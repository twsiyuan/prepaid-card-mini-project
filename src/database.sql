SET NAMES utf8;
SET time_zone = '+00:00';
SET foreign_key_checks = 0;
SET sql_mode = 'NO_AUTO_VALUE_ON_ZERO';

SET NAMES utf8mb4;

DROP TABLE IF EXISTS `captures`;
CREATE TABLE `captures` (
  `captureID` bigint(20) NOT NULL AUTO_INCREMENT,
  `txnID` bigint(20) NOT NULL,
  `amount` decimal(24,2) NOT NULL,
  `createTime` timestamp NOT NULL DEFAULT current_timestamp(),
  PRIMARY KEY (`captureID`),
  KEY `txnID` (`txnID`),
  CONSTRAINT `captures_ibfk_2` FOREIGN KEY (`txnID`) REFERENCES `transactions` (`txnID`) ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;


DROP TABLE IF EXISTS `cards`;
CREATE TABLE `cards` (
  `cardID` bigint(20) NOT NULL AUTO_INCREMENT,
  `name` varchar(100) COLLATE utf8mb4_unicode_520_ci NOT NULL,
  PRIMARY KEY (`cardID`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;


DROP VIEW IF EXISTS `cardsDetail`;
CREATE TABLE `cardsDetail` (`cardID` bigint(20), `name` varchar(100), `loadedAmount` decimal(46,2), `blockedAmount` decimal(65,2));


DROP TABLE IF EXISTS `loads`;
CREATE TABLE `loads` (
  `loadID` bigint(20) NOT NULL AUTO_INCREMENT,
  `cardID` bigint(20) NOT NULL,
  `amount` decimal(24,2) NOT NULL,
  `createTime` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`loadID`),
  KEY `cardID` (`cardID`),
  CONSTRAINT `loads_ibfk_2` FOREIGN KEY (`cardID`) REFERENCES `cards` (`cardID`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;


DROP TABLE IF EXISTS `merchants`;
CREATE TABLE `merchants` (
  `merchantID` bigint(20) NOT NULL AUTO_INCREMENT,
  `name` varchar(100) COLLATE utf8mb4_unicode_520_ci NOT NULL,
  `authToken` varchar(100) CHARACTER SET ascii NOT NULL,
  PRIMARY KEY (`merchantID`),
  UNIQUE KEY `authToken` (`authToken`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;


DROP TABLE IF EXISTS `refunds`;
CREATE TABLE `refunds` (
  `refundID` bigint(20) NOT NULL AUTO_INCREMENT,
  `txnID` bigint(20) NOT NULL,
  `amount` decimal(24,2) NOT NULL,
  `createTime` timestamp NOT NULL DEFAULT current_timestamp(),
  PRIMARY KEY (`refundID`),
  KEY `txnID` (`txnID`),
  CONSTRAINT `refunds_ibfk_2` FOREIGN KEY (`txnID`) REFERENCES `transactions` (`txnID`) ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;


DROP TABLE IF EXISTS `reverses`;
CREATE TABLE `reverses` (
  `reverseID` bigint(20) NOT NULL AUTO_INCREMENT,
  `txnID` bigint(20) NOT NULL,
  `amount` decimal(24,2) NOT NULL,
  `createTime` timestamp NOT NULL DEFAULT current_timestamp(),
  PRIMARY KEY (`reverseID`),
  KEY `txnID` (`txnID`),
  CONSTRAINT `reverses_ibfk_2` FOREIGN KEY (`txnID`) REFERENCES `transactions` (`txnID`) ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;


DROP TABLE IF EXISTS `transactions`;
CREATE TABLE `transactions` (
  `txnID` bigint(20) NOT NULL AUTO_INCREMENT,
  `merchantID` bigint(20) NOT NULL,
  `cardID` bigint(20) NOT NULL,
  `text` varchar(100) COLLATE utf8mb4_unicode_520_ci NOT NULL,
  `amount` decimal(24,2) NOT NULL,
  `createTime` timestamp NOT NULL DEFAULT current_timestamp(),
  PRIMARY KEY (`txnID`),
  KEY `merchantID` (`merchantID`),
  KEY `cardID` (`cardID`),
  CONSTRAINT `transactions_ibfk_3` FOREIGN KEY (`merchantID`) REFERENCES `merchants` (`merchantID`) ON UPDATE CASCADE,
  CONSTRAINT `transactions_ibfk_4` FOREIGN KEY (`cardID`) REFERENCES `cards` (`cardID`) ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;


DROP VIEW IF EXISTS `transactionsDetail`;
CREATE TABLE `transactionsDetail` (`txnID` bigint(20), `merchantID` bigint(20), `cardID` bigint(20), `text` varchar(100), `originalBlockedAmount` decimal(24,2), `reversedAmount` decimal(46,2), `blockedAmount` decimal(47,2), `capturedAmount` decimal(46,2), `waitCaptureAmount` decimal(48,2), `refundedAmount` decimal(46,2));


DROP TABLE IF EXISTS `cardsDetail`;
CREATE ALGORITHM=UNDEFINED SQL SECURITY DEFINER VIEW `cardsDetail` AS select `c`.`cardID` AS `cardID`,`c`.`name` AS `name`,coalesce((select sum(`l`.`amount`) from `prepaid-card-mini-project`.`loads` `l` where `l`.`cardID` = `c`.`cardID`),0) AS `loadedAmount`,coalesce((select sum(`t`.`blockedAmount`) - sum(`t`.`refundedAmount`) from `prepaid-card-mini-project`.`transactionsDetail` `t` where `t`.`cardID` = `c`.`cardID`),0) AS `blockedAmount` from `prepaid-card-mini-project`.`cards` `c`;

DROP TABLE IF EXISTS `transactionsDetail`;
CREATE ALGORITHM=UNDEFINED SQL SECURITY DEFINER VIEW `transactionsDetail` AS select `t`.`txnID` AS `txnID`,`t`.`merchantID` AS `merchantID`,`t`.`cardID` AS `cardID`,`t`.`text` AS `text`,`t`.`originalBlockedAmount` AS `originalBlockedAmount`,`t`.`reversedAmount` AS `reversedAmount`,`t`.`originalBlockedAmount` - `t`.`reversedAmount` AS `blockedAmount`,`t`.`capturedAmount` AS `capturedAmount`,`t`.`originalBlockedAmount` - `t`.`reversedAmount` - `t`.`capturedAmount` AS `waitCaptureAmount`,`t`.`refundedAmount` AS `refundedAmount` from (select `e`.`txnID` AS `txnID`,`e`.`merchantID` AS `merchantID`,`e`.`cardID` AS `cardID`,`e`.`text` AS `text`,`e`.`amount` AS `originalBlockedAmount`,coalesce((select sum(`r`.`amount`) from `prepaid-card-mini-project`.`reverses` `r` where `r`.`txnID` = `e`.`txnID`),0) AS `reversedAmount`,coalesce((select sum(`c`.`amount`) from `prepaid-card-mini-project`.`captures` `c` where `c`.`txnID` = `e`.`txnID`),0) AS `capturedAmount`,coalesce((select sum(`d`.`amount`) from `prepaid-card-mini-project`.`refunds` `d` where `d`.`txnID` = `e`.`txnID`),0) AS `refundedAmount` from `prepaid-card-mini-project`.`transactions` `e`) `t`;
