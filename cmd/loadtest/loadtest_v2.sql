SET SERVEROUTPUT ON SIZE UNLIMITED

-- Create a simple sequence-based random helper table for fast lookups
BEGIN EXECUTE IMMEDIATE 'DROP TABLE loadtest2 PURGE'; EXCEPTION WHEN OTHERS THEN NULL; END;
/

CREATE TABLE loadtest2 (
  id     NUMBER NOT NULL,
  grp    NUMBER NOT NULL,
  val1   NUMBER,
  val2   NUMBER,
  pad    VARCHAR2(100) DEFAULT LPAD('X', 80, 'X'),
  CONSTRAINT loadtest2_pk PRIMARY KEY (id) USING INDEX TABLESPACE STRESS_TS
) TABLESPACE STRESS_TS;

CREATE INDEX loadtest2_grp_idx ON loadtest2(grp) TABLESPACE STRESS_TS;

BEGIN EXECUTE IMMEDIATE 'DROP SEQUENCE loadtest2_seq'; EXCEPTION WHEN OTHERS THEN NULL; END;
/
CREATE SEQUENCE loadtest2_seq START WITH 1 INCREMENT BY 1 CACHE 20000 NOORDER;

-- Seed 200K rows
DECLARE
  TYPE t_ids IS TABLE OF NUMBER;
  TYPE t_nums IS TABLE OF NUMBER;
  v_ids  t_ids := t_ids();
  v_grps t_nums := t_nums();
  v_v1   t_nums := t_nums();
  v_v2   t_nums := t_nums();
  v_batch CONSTANT NUMBER := 5000;
BEGIN
  v_ids.EXTEND(v_batch);
  v_grps.EXTEND(v_batch);
  v_v1.EXTEND(v_batch);
  v_v2.EXTEND(v_batch);

  FOR b IN 1..40 LOOP
    FOR i IN 1..v_batch LOOP
      SELECT loadtest2_seq.NEXTVAL INTO v_ids(i) FROM dual;
      v_grps(i) := MOD(v_ids(i), 1000);
      v_v1(i) := MOD(v_ids(i) * 7, 9999);
      v_v2(i) := MOD(v_ids(i) * 13, 9999);
    END LOOP;

    FORALL i IN 1..v_batch
      INSERT INTO loadtest2 (id, grp, val1, val2)
      VALUES (v_ids(i), v_grps(i), v_v1(i), v_v2(i));

    COMMIT;
  END LOOP;

  DBMS_OUTPUT.PUT_LINE('Seeded ' || (40 * v_batch) || ' rows');
END;
/

SELECT COUNT(*) AS row_count, ROUND(COUNT(*)/1000) AS rows_k FROM loadtest2;

-- Recreate package with high-performance worker
CREATE OR REPLACE PACKAGE loadtest_pkg AS
  PROCEDURE run_worker(p_duration_sec NUMBER DEFAULT 300, p_worker_id NUMBER DEFAULT 0);
  PROCEDURE start_load(p_workers NUMBER DEFAULT 30, p_duration_sec NUMBER DEFAULT 300);
  PROCEDURE stop_load;
  PROCEDURE report;
END loadtest_pkg;
/

CREATE OR REPLACE PACKAGE BODY loadtest_pkg AS

  PROCEDURE run_worker(p_duration_sec NUMBER DEFAULT 300, p_worker_id NUMBER DEFAULT 0) IS
    v_end_time    DATE := SYSDATE + p_duration_sec / 86400;
    v_iter        NUMBER := p_worker_id * 1000000; -- offset per worker
    v_id          NUMBER;
    v_val         NUMBER;
    v_payload     VARCHAR2(100);
    v_max_id      NUMBER;
    v_op          NUMBER;
    v_target      NUMBER;
    v_commit_freq CONSTANT NUMBER := 200;
    v_ins_count   NUMBER := 0;
    v_del_count   NUMBER := 0;
    v_max_rows    CONSTANT NUMBER := 600000;
    v_approx_rows NUMBER;

    -- Bulk arrays for FORALL
    TYPE t_nums IS TABLE OF NUMBER INDEX BY PLS_INTEGER;
    v_bulk_ids  t_nums;
    v_bulk_vals t_nums;
    v_bulk_size CONSTANT NUMBER := 50;
  BEGIN
    -- Get current max id
    SELECT NVL(MAX(id), 0) INTO v_max_id FROM loadtest2;
    SELECT COUNT(*) INTO v_approx_rows FROM loadtest2;

    WHILE SYSDATE < v_end_time LOOP
      v_iter := v_iter + 1;

      -- Cheap pseudo-random: use iter + worker_id for distribution
      v_op := MOD(v_iter, 10);  -- 0-9 determines operation

      CASE
        -- BULK INSERT (20%): ops 0,1
        WHEN v_op <= 1 THEN
          FOR i IN 1..v_bulk_size LOOP
            SELECT loadtest2_seq.NEXTVAL INTO v_bulk_ids(i) FROM dual;
            v_bulk_vals(i) := MOD(v_bulk_ids(i) * 7 + v_iter, 9999);
          END LOOP;
          FORALL i IN 1..v_bulk_size
            INSERT INTO loadtest2 (id, grp, val1, val2)
            VALUES (v_bulk_ids(i), MOD(v_bulk_ids(i), 1000), v_bulk_vals(i), v_bulk_vals(i));
          v_ins_count := v_ins_count + v_bulk_size;
          v_approx_rows := v_approx_rows + v_bulk_size;

        -- BULK DELETE (20%): ops 2,3 - match insert rate
        WHEN v_op <= 3 THEN
          -- Delete oldest rows by ID range
          v_target := MOD(v_iter * 31 + p_worker_id * 7, GREATEST(v_max_id, 1));
          DELETE FROM loadtest2
          WHERE id BETWEEN v_target AND v_target + v_bulk_size
          AND ROWNUM <= v_bulk_size;
          v_del_count := v_del_count + SQL%ROWCOUNT;
          v_approx_rows := v_approx_rows - SQL%ROWCOUNT;

        -- PK SELECT (30%): ops 4,5,6
        WHEN v_op <= 6 THEN
          v_target := MOD(v_iter * 17 + p_worker_id * 13, GREATEST(v_max_id, 1)) + 1;
          BEGIN
            SELECT val1 INTO v_val FROM loadtest2 WHERE id = v_target;
          EXCEPTION WHEN NO_DATA_FOUND THEN NULL;
          END;

        -- INDEX RANGE SELECT (10%): op 7
        WHEN v_op = 7 THEN
          SELECT COUNT(*), NVL(SUM(val1), 0) INTO v_val, v_id
          FROM loadtest2
          WHERE grp = MOD(v_iter, 1000);

        -- BULK UPDATE (20%): ops 8,9
        WHEN v_op >= 8 THEN
          v_target := MOD(v_iter * 23 + p_worker_id * 11, GREATEST(v_max_id, 1)) + 1;
          FOR i IN 1..v_bulk_size LOOP
            v_bulk_ids(i) := v_target + i;
            v_bulk_vals(i) := MOD(v_iter + i, 9999);
          END LOOP;
          FORALL i IN 1..v_bulk_size
            UPDATE loadtest2 SET val1 = v_bulk_vals(i), val2 = v_bulk_vals(i)
            WHERE id = v_bulk_ids(i);
      END CASE;

      -- Periodic commit
      IF MOD(v_iter, v_commit_freq) = 0 THEN
        COMMIT;

        -- Space check: if too many rows, force more deletes
        IF v_approx_rows > v_max_rows THEN
          DELETE FROM loadtest2 WHERE ROWNUM <= 10000;
          v_del_count := v_del_count + SQL%ROWCOUNT;
          v_approx_rows := v_approx_rows - SQL%ROWCOUNT;
          COMMIT;
        END IF;
      END IF;
    END LOOP;

    COMMIT;
  EXCEPTION
    WHEN OTHERS THEN
      COMMIT;
  END run_worker;

  PROCEDURE start_load(p_workers NUMBER DEFAULT 30, p_duration_sec NUMBER DEFAULT 300) IS
    v_job NUMBER;
  BEGIN
    stop_load;
    DBMS_OUTPUT.PUT_LINE('Starting ' || p_workers || ' workers for ' || p_duration_sec || 's');
    FOR i IN 1..p_workers LOOP
      DBMS_JOB.SUBMIT(
        job       => v_job,
        what      => 'loadtest_pkg.run_worker(' || p_duration_sec || ', ' || i || ');',
        next_date => SYSDATE,
        interval  => NULL
      );
    END LOOP;
    COMMIT;
    DBMS_OUTPUT.PUT_LINE('All ' || p_workers || ' workers submitted.');
  END start_load;

  PROCEDURE stop_load IS
    v_count NUMBER := 0;
  BEGIN
    FOR rec IN (SELECT job FROM user_jobs WHERE what LIKE 'loadtest_pkg.run_worker%') LOOP
      BEGIN
        DBMS_JOB.REMOVE(rec.job);
        v_count := v_count + 1;
      EXCEPTION WHEN OTHERS THEN NULL;
      END;
    END LOOP;
    COMMIT;
    IF v_count > 0 THEN
      DBMS_OUTPUT.PUT_LINE('Removed ' || v_count || ' pending jobs');
    END IF;
  END stop_load;

  PROCEDURE report IS
    v_sessions NUMBER;
    v_count    NUMBER;
    v_size_mb  NUMBER;
  BEGIN
    SELECT COUNT(*) INTO v_sessions
    FROM v$session WHERE username = 'OPENDB_TEST' AND status = 'ACTIVE';
    SELECT COUNT(*) INTO v_count FROM loadtest2;
    SELECT NVL(ROUND(bytes/1024/1024), 0) INTO v_size_mb
    FROM user_segments WHERE segment_name = 'LOADTEST2' AND ROWNUM = 1;
    DBMS_OUTPUT.PUT_LINE('Active workers: ' || v_sessions);
    DBMS_OUTPUT.PUT_LINE('Table rows:     ' || v_count);
    DBMS_OUTPUT.PUT_LINE('Table size:     ' || v_size_mb || ' MB');
  END report;

END loadtest_pkg;
/

SHOW ERRORS PACKAGE BODY loadtest_pkg;
