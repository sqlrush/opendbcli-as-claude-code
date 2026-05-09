SET SERVEROUTPUT ON SIZE UNLIMITED

CREATE OR REPLACE PACKAGE BODY loadtest_pkg AS

  PROCEDURE run_worker(p_duration_sec NUMBER DEFAULT 300, p_worker_id NUMBER DEFAULT 0) IS
    v_end_time    DATE := SYSDATE + p_duration_sec / 86400;
    v_iter        NUMBER := 0;
    v_val         NUMBER;
    v_max_id      NUMBER;
    v_op          NUMBER;
    v_target      NUMBER;
    v_approx_rows NUMBER;
    v_max_rows    CONSTANT NUMBER := 500000;
    v_bsz         CONSTANT NUMBER := 15;
    v_commit_freq CONSTANT NUMBER := 2;
    v_grp_base    NUMBER := MOD(p_worker_id * 8, 1000);
    v_grp         NUMBER;

    TYPE t_nums IS TABLE OF NUMBER INDEX BY PLS_INTEGER;
    v_ids   t_nums;
    v_vals  t_nums;
    v_vals2 t_nums;
  BEGIN
    SELECT NVL(MAX(id), 1) INTO v_max_id FROM loadtest2;

    WHILE SYSDATE < v_end_time LOOP
      v_iter := v_iter + 1;
      v_op := MOD(v_iter, 20);
      v_grp := v_grp_base + MOD(v_iter, 8);

      CASE
        -- FORALL INSERT (20%): ops 0-3
        -- Each NEXTVAL + each FORALL row = separate executes
        WHEN v_op <= 3 THEN
          FOR i IN 1..v_bsz LOOP
            SELECT loadtest2_seq.NEXTVAL INTO v_ids(i) FROM dual;
            v_vals(i) := MOD(v_iter * 7 + i, 9999);
          END LOOP;
          FORALL i IN 1..v_bsz
            INSERT INTO loadtest2 (id, grp, val1, val2)
            VALUES (v_ids(i), v_grp, v_vals(i), v_vals(i));

        -- DELETE (20%): ops 4-7
        WHEN v_op <= 7 THEN
          DELETE FROM loadtest2
          WHERE grp = v_grp AND ROWNUM <= v_bsz;

        -- MULTIPLE PK SELECTs (30%): ops 8-13, 3 reads per iteration
        WHEN v_op <= 13 THEN
          FOR s IN 1..3 LOOP
            v_target := MOD(v_iter * (17 + s) + p_worker_id * 131, GREATEST(v_max_id, 1)) + 1;
            BEGIN
              SELECT val1 INTO v_val FROM loadtest2 WHERE id = v_target;
            EXCEPTION WHEN NO_DATA_FOUND THEN NULL;
            END;
          END LOOP;

        -- RANGE SELECT (5%): op 14
        WHEN v_op = 14 THEN
          SELECT COUNT(*) INTO v_val FROM loadtest2 WHERE grp = v_grp;

        -- FORALL UPDATE (25%): ops 15-19
        WHEN v_op >= 15 THEN
          v_target := MOD(v_iter * 23 + p_worker_id * 11, GREATEST(v_max_id, 1)) + 1;
          FOR i IN 1..v_bsz LOOP
            v_ids(i) := v_target + i;
            v_vals(i) := MOD(v_iter + i, 9999);
          END LOOP;
          FORALL i IN 1..v_bsz
            UPDATE loadtest2 SET val1 = v_vals(i), val2 = v_vals(i)
            WHERE id = v_ids(i);
      END CASE;

      IF MOD(v_iter, v_commit_freq) = 0 THEN
        COMMIT;
      END IF;

      IF MOD(v_iter, 5000) = 0 THEN
        SELECT COUNT(*) INTO v_approx_rows FROM loadtest2;
        IF v_approx_rows > v_max_rows THEN
          DELETE FROM loadtest2 WHERE grp BETWEEN v_grp_base AND v_grp_base + 7 AND ROWNUM <= 5000;
          COMMIT;
        END IF;
        SELECT NVL(MAX(id), 1) INTO v_max_id FROM loadtest2;
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
