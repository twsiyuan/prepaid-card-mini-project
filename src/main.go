package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/NYTimes/gziphandler"
	"github.com/gorilla/mux"

	_ "github.com/go-sql-driver/mysql"
)

func DbConn(str string) (*sql.DB, error) {
	return sql.Open("mysql", str)
}

func main() {
	dbEndpoint := os.Getenv("DATABASE_ENDPOINT")
	port := os.Getenv("PORT")
	db, err := DbConn(dbEndpoint)
	if err != nil {
		log.Fatal(err)
	}

	// TODO: Not found handleri
	r := mux.NewRouter()

	ar := r.PathPrefix("/api").Subrouter()
	mr := ar.PathPrefix("/merchants/transactions").Subrouter()
	mr.Handle("/", execTxnHandler(db)).Methods(http.MethodPost)
	mtr := mr.PathPrefix("/{id:[1-9][0-9]*}").Subrouter()
	mtr.Handle("/capture", execCaptureHandler("id", db)).Methods(http.MethodPost)
	mtr.Handle("/refund", execRefundHandler("id", db)).Methods(http.MethodPost)
	mtr.Handle("/reverse", execReverseHandler("id", db)).Methods(http.MethodPost)

	cr := ar.PathPrefix("/cards").Subrouter()
	cr.Handle("/", queryCardsHandler(db)).Methods(http.MethodGet)
	cr.Handle("/", execCreateCardHandler(db)).Methods(http.MethodPost)
	cr.Handle("/{id:[1-9][0-9]*}", queryCardHandler("id", db)).Methods(http.MethodGet)
	cr.Handle("/{id:[1-9][0-9]*}", execPutLoadHandler("id", db)).Methods(http.MethodPost)

	r.PathPrefix("/demo/").Handler(http.StripPrefix("/demo/", http.FileServer(http.Dir("static/"))))
	r.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/demo/", http.StatusTemporaryRedirect)
	})
	r.Handle("/statement/{id:[1-9][0-9]*}", exportStatementHandler("id", db))

	h := recoveryHandler(true, gziphandler.GzipHandler(r))

	// TODO: HTTPS
	fmt.Fprintf(os.Stdout, "Try listening...:%s", port)
	if err := http.ListenAndServe(":"+port, h); err != nil {
		fmt.Fprintf(os.Stderr, "Failed, %v\n", err)
	}
}
