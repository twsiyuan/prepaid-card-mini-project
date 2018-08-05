package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/gorilla/mux"
	"github.com/unrolled/render"
)

var (
	ren = render.New()
)

type key int

const (
	keyMerchantID key = iota
	keyCardID
	keyTxnID
)

type responseError struct {
	Error string
}

type amount struct {
	// TODO: Need precision, consider big.Rat in the future
	Amount float64
}

func recoveryHandler(outputErr bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				stack := make([]byte, 2048)
				stack = stack[:runtime.Stack(stack, false)]
				displayErr := "Internal server error"
				if outputErr {
					displayErr = fmt.Sprintf("Unexpected error: %v, in %s", err, stack)
				}

				log.SetOutput(os.Stderr)
				log.Printf("Unexpected error: %v, in %s\n", err, stack)

				ren.JSON(w, http.StatusInternalServerError, responseError{displayErr})
			}
		}()

		next.ServeHTTP(w, req)
	})
}

func merchantAuthMiddleware(db *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		token := req.Header.Get("Authorization")
		if len(token) > 0 {
			var mID int64
			if err := db.QueryRowContext(req.Context(), "SELECT `merchantID` FROM `merchants` WHERE `authToken` = ?", token).Scan(&mID); err != nil {
				panic(err)
			}

			ctx := context.WithValue(req.Context(), keyMerchantID, mID)
			next.ServeHTTP(w, req.WithContext(ctx))
			return
		}

		ren.JSON(w, http.StatusUnauthorized, responseError{
			"Unauthorized",
		})
	})
}

func txnMiddleware(routerName string, db *sql.DB, next http.Handler) http.Handler {
	return merchantAuthMiddleware(db, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		tData := mux.Vars(req)[routerName]
		if len(tData) <= 0 {
			ren.JSON(w, http.StatusNotFound, responseError{"Not found"})
			return
		}

		mID := req.Context().Value(keyMerchantID).(int64)
		txnID := int64(0)
		if err := db.QueryRowContext(req.Context(), "SELECT `txnID` FROM `transactions` WHERE `txnID`=? AND `merchantID`=?", tData, mID).Scan(&txnID); err != nil {
			if err == sql.ErrNoRows {
				ren.JSON(w, http.StatusNotFound, responseError{"Not found"})
				return
			}
			panic(err)
		}

		ctx := context.WithValue(req.Context(), keyTxnID, txnID)
		next.ServeHTTP(w, req.WithContext(ctx))
	}))
}

func execTxnHandler(db *sql.DB) http.Handler {
	return merchantAuthMiddleware(db, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mID := req.Context().Value(keyMerchantID).(int64)

		b, err := ioutil.ReadAll(req.Body)
		if err != nil {
			ren.JSON(w, http.StatusBadRequest, "No request body")
			return
		}
		req.Body.Close()

		// TODO: security issue, should use virtual card id here
		var v struct {
			Amount float64
			CardID int64
			Text   string
		}
		if err := json.Unmarshal(b, &v); err != nil {
			ren.JSON(w, http.StatusBadRequest, "Invalid request body")
		}

		if v.Amount <= 0 {
			ren.JSON(w, http.StatusBadRequest, "Invalid amount")
		}

		tx, err := db.BeginTx(req.Context(), nil)
		if err != nil {
			panic(err)
		}
		defer tx.Rollback()

		if err := tx.QueryRowContext(req.Context(), "SELECT `cardID` FROM `cards` WHERE `cardID` = ? FOR UPDATE", v.CardID).Scan(&v.CardID); err != nil {
			if err == sql.ErrNoRows {
				ren.JSON(w, http.StatusBadRequest, responseError{"Card is invalid"})
				return
			}
			panic(err)
		}

		ok := false
		if err := tx.QueryRowContext(req.Context(), "SELECT `loadedAmount` - `blockedAmount` > ? FROM `cardsDetail` WHERE `cardID` = ?", v.Amount, v.CardID).Scan(&ok); err != nil {
			panic(err)
		}

		if !ok {
			ren.JSON(w, http.StatusForbidden, responseError{
				"Insufficient amount",
			})
			return
		}

		res, err := tx.ExecContext(req.Context(), "INSERT INTO `transactions`(`merchantID`, `cardID`, `amount`, `text`)VALUES(?, ?, ?, ?)", mID, v.CardID, v.Amount, v.Text)
		if err != nil {
			panic(err)
		}

		id, err := res.LastInsertId()
		if err != nil {
			panic(err)
		}

		if err := tx.Commit(); err != nil {
			panic(err)
		}

		ren.JSON(w, http.StatusOK, struct {
			TxnID int64
		}{
			id,
		})
	}))
}

func execRefundHandler(routerName string, db *sql.DB) http.Handler {
	return txnMiddleware(routerName, db, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		txnID := req.Context().Value(keyTxnID)
		v, err := readAmountFromBody(req.Body)
		if err != nil {
			ren.JSON(w, http.StatusBadRequest, responseError{err.Error()})
			return
		}

		tx, err := db.BeginTx(req.Context(), nil)
		if err != nil {
			panic(err)
		}
		defer tx.Rollback()

		if err := tx.QueryRowContext(req.Context(), "SELECT `txnID` FROM `transactions` WHERE `txnID`=? FOR UPDATE", txnID).Scan(&txnID); err != nil {
			panic(err)
		}

		var ok bool
		if err := tx.QueryRowContext(req.Context(), "SELECT `capturedAmount` - `refundedAmount` >= ? FROM `transactionsDetail` WHERE `txnID` = ?", v.Amount, txnID).Scan(&ok); err != nil {
			panic(err)
		}

		if !ok {
			ren.JSON(w, http.StatusForbidden, responseError{
				"Cannot refund (out of allowed amount)",
			})
			return
		}

		var id int64
		if res, err := tx.ExecContext(req.Context(), "INSERT INTO `refunds`(`txnID`, `amount`)VALUES(?, ?)", txnID, v.Amount); err != nil {
			panic(err)
		} else if id, err = res.LastInsertId(); err != nil {
			panic(err)
		}

		if err := tx.Commit(); err != nil {
			panic(err)
		}
		ren.JSON(w, http.StatusOK, struct {
			RefundID int64
		}{
			id,
		})
	}))
}

func execCaptureHandler(routerName string, db *sql.DB) http.Handler {
	return txnMiddleware(routerName, db, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		txnID := req.Context().Value(keyTxnID)
		v, err := readAmountFromBody(req.Body)
		if err != nil {
			ren.JSON(w, http.StatusBadRequest, responseError{err.Error()})
			return
		}

		tx, err := db.BeginTx(req.Context(), nil)
		if err != nil {
			panic(err)
		}
		defer tx.Rollback()

		if err := tx.QueryRowContext(req.Context(), "SELECT `txnID` FROM `transactions` WHERE `txnID`=? FOR UPDATE", txnID).Scan(&txnID); err != nil {
			panic(err)
		}

		var ok bool
		if err := tx.QueryRowContext(req.Context(), "SELECT `waitCaptureAmount` >= ? FROM `transactionsDetail` WHERE `txnID` = ?", v.Amount, txnID).Scan(&ok); err != nil {
			panic(err)
		}

		if !ok {
			ren.JSON(w, http.StatusForbidden, responseError{
				"Cannot capture (out of allowed amount)",
			})
			return
		}

		var id int64
		if res, err := tx.ExecContext(req.Context(), "INSERT INTO `captures`(`txnID`, `amount`)VALUES(?, ?)", txnID, v.Amount); err != nil {
			panic(err)
		} else if id, err = res.LastInsertId(); err != nil {
			panic(err)
		}

		if err := tx.Commit(); err != nil {
			panic(err)
		}
		ren.JSON(w, http.StatusOK, struct {
			CaptureID int64
		}{
			id,
		})
	}))
}

func execReverseHandler(routerName string, db *sql.DB) http.Handler {
	return txnMiddleware(routerName, db, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		txnID := req.Context().Value(keyTxnID)
		v, err := readAmountFromBody(req.Body)
		if err != nil {
			ren.JSON(w, http.StatusBadRequest, responseError{err.Error()})
			return
		}

		tx, err := db.BeginTx(req.Context(), nil)
		if err != nil {
			panic(err)
		}
		defer tx.Rollback()

		if err := tx.QueryRowContext(req.Context(), "SELECT `txnID` FROM `transactions` WHERE `txnID`=? FOR UPDATE", txnID).Scan(&txnID); err != nil {
			panic(err)
		}

		var ok bool
		if err := tx.QueryRowContext(req.Context(), "SELECT `blockedAmount` >= ? FROM `transactionsDetail` WHERE `txnID` = ?", v.Amount, txnID).Scan(&ok); err != nil {
			panic(err)
		}

		if !ok {
			ren.JSON(w, http.StatusForbidden, responseError{
				"Cannot reverse (out of blocked amount)",
			})
			return
		}

		var id int64
		if res, err := tx.ExecContext(req.Context(), "INSERT INTO `reverses`(`txnID`, `amount`)VALUES(?, ?)", txnID, v.Amount); err != nil {
			panic(err)
		} else if id, err = res.LastInsertId(); err != nil {
			panic(err)
		}

		if err := tx.Commit(); err != nil {
			panic(err)
		}
		ren.JSON(w, http.StatusOK, struct {
			ReverseID int64
		}{
			id,
		})
	}))
}

func cardMiddleware(routerName string, db *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cData := mux.Vars(req)[routerName]
		if len(cData) <= 0 {
			ren.JSON(w, http.StatusNotFound, responseError{"Not found"})
			return
		}

		var cardID int64
		if err := db.QueryRowContext(req.Context(), "SELECT `cardID` FROM `cards` WHERE `cardID`=?", cData).Scan(&cardID); err != nil {
			if err == sql.ErrNoRows {
				ren.JSON(w, http.StatusNotFound, responseError{"Not found"})
				return
			}
			panic(err)
		}

		ctx := context.WithValue(req.Context(), keyCardID, cardID)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func readAmountFromBody(body io.ReadCloser) (*amount, error) {
	b, err := ioutil.ReadAll(body)
	if err != nil {
		return nil, errors.New("No request body")
	}
	body.Close()

	var v amount
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, errors.New("Invalid request body")
	}

	if v.Amount <= 0 {
		return nil, errors.New("Invalid amount")
	}

	return &v, nil
}

func execCreateCardHandler(db *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, err := ioutil.ReadAll(req.Body)
		if err != nil {
			ren.JSON(w, http.StatusBadRequest, "No request body")
			return
		}
		req.Body.Close()

		var v struct {
			Name string
		}
		if err := json.Unmarshal(b, &v); err != nil {
			ren.JSON(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		res, err := db.ExecContext(req.Context(), "INSERT INTO `cards`(`name`)VALUES(?)", v.Name)
		if err != nil {
			panic(err)
		}

		id, err := res.LastInsertId()
		if err != nil {
			panic(err)
		}

		ren.JSON(w, http.StatusOK, struct {
			CardID int64
		}{
			id,
		})
	})
}

func execPutLoadHandler(routerName string, db *sql.DB) http.Handler {
	return cardMiddleware(routerName, db, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cardID := req.Context().Value(keyCardID).(int64)
		v, err := readAmountFromBody(req.Body)
		if err != nil {
			ren.JSON(w, http.StatusBadRequest, responseError{err.Error()})
			return
		}

		if result, err := db.ExecContext(req.Context(), "INSERT INTO `loads`(`cardID`, `amount`)VALUES(?, ?)", cardID, v.Amount); err != nil {
			panic(err)
		} else if id, err := result.LastInsertId(); err != nil {
			panic(err)
		} else {
			ren.JSON(w, http.StatusOK, struct {
				LoadID int64
			}{
				id,
			})
		}
	}))
}

type prepaidCard struct {
	CardID           int64
	Name             string
	AvailableBalance float64
	BlockedAmount    float64
}

// TODO: paging
func queryCardsHandler(db *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rows, err := db.QueryContext(req.Context(), "SELECT `cardID`, `name`, `loadedAmount` - `blockedAmount`,`blockedAmount` FROM `cardsDetail`")
		if err != nil {
			panic(err)
		}

		cs := make([]prepaidCard, 0)
		for rows.Next() {
			c := prepaidCard{}
			if err := rows.Scan(&c.CardID, &c.Name, &c.AvailableBalance, &c.BlockedAmount); err != nil {
				panic(err)
			}
			cs = append(cs, c)
		}

		ren.JSON(w, http.StatusOK, cs)
	})
}

func queryCardHandler(routerName string, db *sql.DB) http.Handler {
	return cardMiddleware(routerName, db, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cardID := req.Context().Value(keyCardID).(int64)

		// TODO: precition issue
		c := prepaidCard{}
		if err := db.QueryRowContext(req.Context(), "SELECT `cardID`, `name`, `loadedAmount` - `blockedAmount`,`blockedAmount` FROM `cardsDetail` WHERE `cardID` = ?", cardID).Scan(&c.CardID, &c.Name, &c.AvailableBalance, &c.BlockedAmount); err != nil {
			panic(err)
		}

		ren.JSON(w, http.StatusOK, c)
	}))
}

func exportStatementHandler(routerName string, db *sql.DB) http.Handler {
	return cardMiddleware(routerName, db, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cardID := req.Context().Value(keyCardID).(int64)

		// Export as CSV
		buf := bytes.NewBufferString("Date,Text,Location,Amount\n")

		raws, err := db.QueryContext(req.Context(), "SELECT DATE(`t`.`createTime`), `t`.`text`, `m`.`name`, `t`.`amount` FROM `transactions` AS `t` LEFT JOIN `merchants` AS `m` ON `t`.`merchantID` = `m`.`merchantID` WHERE `cardID` = ?", cardID)
		if err != nil {
			panic(err)
		}

		for raws.Next() {
			var date time.Time
			var text, location string
			var amount float64

			if err := raws.Scan(&date, &text, &location, &amount); err != nil {
				panic(err)
			}

			buf.WriteString(fmt.Sprintf("%s,%s,%s,%.2f\n", date.Format("2006-01-02"), text, location, amount))
		}

		w.Header().Add("CONTENT-TYPE", "text/csv")
		w.Write(buf.Bytes())
	}))
}
