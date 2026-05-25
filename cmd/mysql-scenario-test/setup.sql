-- MySQL Scenario Test: Database & Table Setup
-- Run: mysql -u root -p < setup.sql

CREATE DATABASE IF NOT EXISTS mytest CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE mytest;

-- ── lock_test: Used for lock/blocking/DDL scenarios ────────────��───────────
DROP TABLE IF EXISTS lock_test;
CREATE TABLE lock_test (
    id     INT AUTO_INCREMENT PRIMARY KEY,
    value  INT DEFAULT 0,
    status VARCHAR(50) DEFAULT 'active'
) ENGINE=InnoDB;

INSERT INTO lock_test (id, value, status)
SELECT seq, FLOOR(RAND()*1000), ELT(1+FLOOR(RAND()*3), 'active','pending','done')
FROM (SELECT @row := @row + 1 AS seq FROM
      (SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) a,
      (SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) b,
      (SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) c,
      (SELECT @row := 0) r) t
WHERE seq <= 100;

-- ── big_table: Used for SQL performance / IO scenarios ────────��────────────
DROP TABLE IF EXISTS big_table;
CREATE TABLE big_table (
    id          INT AUTO_INCREMENT PRIMARY KEY,
    value       INT DEFAULT 0,
    status      VARCHAR(50) DEFAULT 'active',
    varchar_col VARCHAR(200) DEFAULT '',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_status (status),
    INDEX idx_created (created_at, status)
) ENGINE=InnoDB;

-- Insert 500K rows in batches
DELIMITER //
CREATE PROCEDURE fill_big_table()
BEGIN
    DECLARE i INT DEFAULT 0;
    WHILE i < 500 DO
        INSERT INTO big_table (value, status, varchar_col, created_at)
        SELECT
            FLOOR(RAND()*10000),
            ELT(1+FLOOR(RAND()*5), 'active','active','active','pending','done'),
            CONCAT('row_', FLOOR(RAND()*1000000), '_data_', REPEAT('x', 50+FLOOR(RAND()*100))),
            DATE_ADD('2025-01-01', INTERVAL FLOOR(RAND()*365*2) DAY)
        FROM (SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) a,
             (SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) b,
             (SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) c,
             (SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) d;
        SET i = i + 1;
    END WHILE;
END //
DELIMITER ;

CALL fill_big_table();
DROP PROCEDURE fill_big_table;

SELECT COUNT(*) AS big_table_rows FROM big_table;

-- ── parent_table + child_table: FK scenarios ───────────────────────────────
DROP TABLE IF EXISTS child_table;
DROP TABLE IF EXISTS parent_table;

CREATE TABLE parent_table (
    id   INT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(100) DEFAULT 'parent'
) ENGINE=InnoDB;

INSERT INTO parent_table (id, name) VALUES (1,'parent_1'),(2,'parent_2'),(3,'parent_3');

CREATE TABLE child_table (
    id        INT AUTO_INCREMENT PRIMARY KEY,
    parent_id INT,
    data      VARCHAR(100) DEFAULT 'child',
    FOREIGN KEY (parent_id) REFERENCES parent_table(id) ON DELETE CASCADE
    -- Intentionally NO INDEX on parent_id for T010/T098
) ENGINE=InnoDB;

INSERT INTO child_table (parent_id, data)
SELECT
    ELT(1+FLOOR(RAND()*3), 1, 2, 3),
    CONCAT('child_', FLOOR(RAND()*100000))
FROM (SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) a,
     (SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) b,
     (SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) c,
     (SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) d;

-- ── idx_test: Table with many indexes for write-pressure scenarios ─────────
DROP TABLE IF EXISTS idx_test;
CREATE TABLE idx_test (
    id   INT AUTO_INCREMENT PRIMARY KEY,
    col1 DOUBLE,
    col2 DOUBLE,
    col3 DOUBLE,
    col4 DOUBLE,
    col5 DOUBLE,
    col6 DOUBLE,
    INDEX idx1 (col1),
    INDEX idx2 (col2),
    INDEX idx3 (col3),
    INDEX idx4 (col4),
    INDEX idx5 (col5),
    INDEX idx6 (col6),
    INDEX idx12 (col1, col2),
    INDEX idx23 (col2, col3),
    INDEX idx34 (col3, col4),
    INDEX idx45 (col4, col5),
    INDEX idx56 (col5, col6),
    INDEX idx123 (col1, col2, col3)
) ENGINE=InnoDB;

-- ── Ensure performance_schema is configured ────────────────────────────────
-- These should already be ON by default in MySQL 8.0+

-- Enable data_locks consumer (needed for blocking chain detection)
UPDATE performance_schema.setup_instruments SET ENABLED='YES', TIMED='YES'
WHERE NAME LIKE 'wait/lock/metadata/sql/%' OR NAME LIKE 'wait/io/%' OR NAME LIKE 'wait/synch/%';

UPDATE performance_schema.setup_consumers SET ENABLED='YES'
WHERE NAME IN ('events_waits_current', 'events_waits_summary_by_event_name',
               'events_statements_summary_by_digest', 'global_instrumentation');

-- Verify setup
SELECT 'Setup complete!' AS status;
SELECT COUNT(*) AS lock_test_rows FROM lock_test;
SELECT COUNT(*) AS big_table_rows FROM big_table;
SELECT COUNT(*) AS parent_rows FROM parent_table;
SELECT COUNT(*) AS child_rows FROM child_table;
