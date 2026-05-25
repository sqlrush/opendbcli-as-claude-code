# P1 优化前后对比 (真实终端输出)

> 测试环境: Oracle 19.3.0.0.0, oratest, TermWidth=120

---

## /sessions

### 优化前
```
┌──────┬─────────┬──────────┬────────┬────────┬──────────┬───────────┬───────────┬───────────┬───────────┬───────────┐
  │ SID  │ SERIAL# │ USERNAME │ STATUS │ OSUSER │ MACHINE  │ PROGRAM   │ SQL_ID    │ EVENT     │ WAIT_C... │ SECOND... │
  ├──────┼─────────┼──────────┼────────┼────────┼──────────┼───────────┼───────────┼───────────┼───────────┼───────────┤
  │ 431  │ 10054   │ OPEND... │ ACTIVE │ oracle │ oracl... │ opendb    │ 11b9k2... │ SQL*Ne... │ Network   │ 0         │
  │ 1705 │ 12343   │ SYS      │ ACTIVE │ oracle │ oracl... │ oracle... │ NULL      │ OFS idle  │ Idle      │ 2         │
  │ 1    │ 15066   │ NULL     │ ACTIVE │ oracle │ oracl... │ oracle... │ NULL      │ wait f... │ Idle      │ 0         │
  │ 2    │ 1677    │ NULL     │ ACTIVE │ oracle │ oracl... │ oracle... │ NUL
```

### 优化后
```
┌──────┬─────────┬─────────────┬────────┬────────────┬─────────────┬─────────────┬──────────────┬─────────┬──────────┐
  │ SID  │ SERIAL# │ USERNAME    │ STATUS │ MACHINE    │ PROGRAM     │ SQL_ID      │ EVENT        │ WCLASS  │ WAIT_SEC │
  ├──────┼─────────┼─────────────┼────────┼────────────┼─────────────┼─────────────┼──────────────┼─────────┼──────────┤
  │ 431  │ 3179    │ OPENDB_TEST │ ACTIVE │ oracledb01 │ opendb      │ 86m6v287... │ SQL*Net m... │ Network │ 0        │
  │ 1705 │ 12343   │ SYS         │ ACTIVE │ oracledb01 │ oracle@o... │ NULL        │ OFS idle     │ Idle    │ 2        │
  │ 1    │ 15066   │ NULL        │ ACTIVE │ oracledb01 │ oracle@o... │ NULL        │ wait for ... │ Idle    │ 0        │
  │ 2    │ 1677    │ NULL        │ ACTIVE │ oracledb01 │ oracle@o... │ NULL 
```

---

## /activesessions

### 优化前
```
┌──────┬─────────┬──────────┬────────┬────────┬──────────┬───────────┬───────────┬───────────┬───────────┬───────────┐
  │ SID  │ SERIAL# │ USERNAME │ STATUS │ OSUSER │ MACHINE  │ PROGRAM   │ SQL_ID    │ EVENT     │ WAIT_C... │ SECOND... │
  ├──────┼─────────┼──────────┼────────┼────────┼──────────┼───────────┼───────────┼───────────┼───────────┼───────────┤
  │ 1705 │ 12343   │ SYS      │ ACTIVE │ oracle │ oracl... │ oracle... │ NULL      │ OFS idle  │ Idle      │ 2         │
  │ 431  │ 46393   │ OPEND... │ ACTIVE │ oracle │ oracl... │ opendb    │ 8v7u50... │ SQL*Ne... │ Network   │ 0         │
  └──────┴─────────┴──────────┴────────┴────────┴──────────┴───────────┴───────────┴───────────┴───────────┴───────────┘

2 active sessions found
```

### 优化后
```
┌──────┬─────────┬─────────────┬────────┬────────────┬─────────────┬─────────────┬──────────────┬─────────┬──────────┐
  │ SID  │ SERIAL# │ USERNAME    │ STATUS │ MACHINE    │ PROGRAM     │ SQL_ID      │ EVENT        │ WCLASS  │ WAIT_SEC │
  ├──────┼─────────┼─────────────┼────────┼────────────┼─────────────┼─────────────┼──────────────┼─────────┼──────────┤
  │ 1705 │ 12343   │ SYS         │ ACTIVE │ oracledb01 │ oracle@o... │ NULL        │ OFS idle     │ Idle    │ 2        │
  │ 431  │ 34502   │ OPENDB_TEST │ ACTIVE │ oracledb01 │ opendb      │ ajrxasf3... │ SQL*Net m... │ Network │ 0        │
  └──────┴─────────┴─────────────┴────────┴────────────┴─────────────┴─────────────┴──────────────┴─────────┴──────────┘

2 active sessions found
```

---

## /waits

### 优化前
```
┌────────────────────────────────┬───────────────┬─────────────┬─────────────────┬─────────────┐
  │ EVENT                          │ WAIT_CLASS    │ TOTAL_WAITS │ TIME_WAITED_SEC │ AVG_WAIT_MS │
  ├────────────────────────────────┼───────────────┼─────────────┼─────────────────┼─────────────┤
  │ control file sequential read   │ System I/O    │ 60389       │ 35.42           │ 0           │
  │ control file parallel write    │ System I/O    │ 9103        │ 5.97            │ 0           │
  │ Data file init write           │ User I/O      │ 258         │ 5.93            │ 0.02        │
  │ latch free                     │ Other         │ 26936       │ 4.92            │ 0           │
  │ db file sequential read        │ User I/O      │ 7243        │ 4.15            │ 0           │
  │ contro
```

### 优化后
```
┌────────────────────────────────┬───────────────┬─────────────┬─────────────────┬─────────────┬─────┐
  │ EVENT                          │ WAIT_CLASS    │ TOTAL_WAITS │ TIME_WAITED_SEC │ AVG_WAIT_MS │ PCT │
  ├────────────────────────────────┼───────────────┼─────────────┼─────────────────┼─────────────┼─────┤
  │ control file sequential read   │ System I/O    │ 63285       │ 37.11           │ 0.586       │ 48  │
  │ control file parallel write    │ System I/O    │ 9554        │ 6.25            │ 0.654       │ 8.1 │
  │ Data file init write           │ User I/O      │ 258         │ 5.93            │ 22.969      │ 7.7 │
  │ latch free                     │ Other         │ 28157       │ 5.09            │ 0.181       │ 6.6 │
  │ db file sequential read        │ User I/O      │ 7292        │ 
```

---

## /latches

### 优化前
```
┌─────────────────────────────────┬─────────┬────────┬────────┬───────────┬────────────┐
  │ NAME                            │ GETS    │ MISSES │ SLEEPS │ SPIN_GETS │ MISS_RATIO │
  ├─────────────────────────────────┼─────────┼────────┼────────┼───────────┼────────────┤
  │ space background task latch     │ 55683   │ 35262  │ 29352  │ 9011      │ 63.33      │
  │ active checkpoint queue latch   │ 68074   │ 1014   │ 401    │ 614       │ 1.49       │
  │ process allocation              │ 2643    │ 181    │ 175    │ 8         │ 6.85       │
  │ qmn task queue latch            │ 4677    │ 661    │ 53     │ 613       │ 14.13      │
  │ post/wait queue                 │ 193750  │ 12483  │ 24     │ 12459     │ 6.44       │
  │ messages                        │ 488933  │ 1335   │ 23     │ 1312    
```

### 优化后
```
┌─────────────────────────────────┬─────────┬────────┬────────┬───────────┬────────────┐
  │ NAME                            │ GETS    │ MISSES │ SLEEPS │ SPIN_GETS │ MISS_RATIO │
  ├─────────────────────────────────┼─────────┼────────┼────────┼───────────┼────────────┤
  │ space background task latch     │ 58380   │ 36990  │ 30743  │ 9522      │ 63.36      │
  │ active checkpoint queue latch   │ 71194   │ 1018   │ 401    │ 618       │ 1.43       │
  │ process allocation              │ 2722    │ 181    │ 175    │ 8         │ 6.65       │
  │ qmn task queue latch            │ 4840    │ 661    │ 53     │ 613       │ 13.66      │
  │ post/wait queue                 │ 203322  │ 12954  │ 28     │ 12926     │ 6.37       │
  │ messages                        │ 511684  │ 1335   │ 23     │ 1312    
```

---

## /mutexes

### 优化前
```
┌───────────────┬─────────────────────────────────┬────────┬───────────┐
  │ MUTEX_TYPE    │ LOCATION                        │ SLEEPS │ WAIT_TIME │
  ├───────────────┼─────────────────────────────────┼────────┼───────────┤
  │ Cursor Pin    │ kkslce [KKSCHLPIN2]             │ 47     │ 50018     │
  │ Row Cache     │ [19] kqrpre                     │ 42     │ 49254     │
  │ Row Cache     │ [10] kqreqd                     │ 41     │ 10173     │
  │ Library Cache │ kglhdgn2 106                    │ 22     │ 58373     │
  │ Library Cache │ kgllkc1   57                    │ 10     │ 9558      │
  │ Library Cache │ kglpin1   4                     │ 9      │ 863       │
  │ Row Cache     │ [17] kqrCreateUsingSecondaryKey │ 7      │ 14        │
  │ Library Cache │ kglhdgh3     161                
```

### 优化后
```
┌───────────────┬─────────────────────────────────┬────────┬───────────┐
  │ MUTEX_TYPE    │ LOCATION                        │ SLEEPS │ WAIT_TIME │
  ├───────────────┼─────────────────────────────────┼────────┼───────────┤
  │ Cursor Pin    │ kkslce [KKSCHLPIN2]             │ 47     │ 50018     │
  │ Row Cache     │ [19] kqrpre                     │ 45     │ 58473     │
  │ Row Cache     │ [10] kqreqd                     │ 44     │ 10194     │
  │ Library Cache │ kglhdgn2 106                    │ 22     │ 58373     │
  │ Library Cache │ kgllkc1   57                    │ 10     │ 9558      │
  │ Library Cache │ kglpin1   4                     │ 9      │ 863       │
  │ Row Cache     │ [17] kqrCreateUsingSecondaryKey │ 7      │ 14        │
  │ Library Cache │ kglhdgh3     161                
```

---

## /health

### 优化前
```
Health Report — orcl (Oracle 19.3.0.0.0)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

OK   Instance       UP (6.3 小时)
OK   Space          All tablespaces within limits
OK   Connections    84 / 1500 (6%)
WARN Slow SQL       4 queries above 5s
OK   Wait Events    No anomalies
WARN Backup         No backup records found
OK   Alert Log      最近24h无告警
OK   Standby        Role: PRIMARY, Switchover: NOT ALLOWED

6 OK, 2 WARN, 0 FAIL
```

### 优化后
```
Health Report — orcl (Oracle 19.3.0.0.0)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

OK   Instance       UP (6.6 小时)
OK   Space          All tablespaces within limits
OK   Connections    84 / 1500 (6%)
WARN Slow SQL       4 queries above 5s
OK   Wait Events    No anomalies
WARN Backup         No backup records found
OK   Alert Log      最近24h无告警
OK   Standby        Role: PRIMARY, Switchover: NOT ALLOWED

6 OK, 2 WARN, 0 FAIL
```

---

## /tempsess

### 优化前
```
临时空间占用会话 (共 4 个, 合计 4 MB):

 SID    USER          TEMP_MB  SQL_ID         EVENT                          STATUS
 ────────────────────────────────────────────────────────────────────────────────
    575                     1                 class slave wait               ACTIVE
   1991                     1                 class slave wait               ACTIVE
   1000                     1                 class slave wait               ACTIVE
    714                     1                 class slave wait               ACTIVE

提示: /kill 575 终止占用最大的会话
4 sessions, 4 MB temp
```

### 优化后
```
┌──────┬──────────────────────────┬─────────┬────────┬──────────────────┬────────┐
  │ SID  │ USERNAME                 │ TEMP_MB │ SQL_ID │ EVENT            │ STATUS │
  ├──────┼──────────────────────────┼─────────┼────────┼──────────────────┼────────┤
  │ 575  │ oracle@oracledb01 (M002) │ 1       │ NULL   │ class slave wait │ ACTIVE │
  │ 1991 │ oracle@oracledb01 (M001) │ 1       │ NULL   │ class slave wait │ ACTIVE │
  │ 714  │ oracle@oracledb01 (M003) │ 1       │ NULL   │ class slave wait │ ACTIVE │
  └──────┴──────────────────────────┴─────────┴────────┴──────────────────┴────────┘

3 sessions, 3 MB temp
合计 3 MB | 提示: /kill 575 终止占用最大的会话
```

---

## /pga

### 优化前
```
PGA 内存概览:

 指标                                 值
 ─────────────────────────────────────────────
 aggregate PGA auto target           14663.0 MB
 aggregate PGA target parameter      16576.0 MB
 maximum PGA allocated                 360.9 MB
 over allocation count                   0.0 MB
 total PGA allocated                   354.9 MB
 total PGA used for auto workareas       0.0 MB
 total freeable PGA memory              30.1 MB

Top PGA 会话:

 SID      USER               PGA_MB   SQL_ID          PROGRAM
 ──────────────────────────────────────────────────────────────────────
 1705     SYS                   3.0                   oracle@oracledb01 (OFSD)
 431      OPENDB_TEST           2.9   frumv2gy7n85k   opendb

提示: /alter pga_aggregate_target 2G 扩大PGA

7 PGA stats, top 2 sessions
```

### 优化后
```
PGA 内存概览:

 指标                                 值
 ─────────────────────────────────────────────
 aggregate PGA auto target           14665.6 MB
 aggregate PGA target parameter      16576.0 MB
 maximum PGA allocated                 360.9 MB
 over allocation count                   0.0 MB
 total PGA allocated                   343.9 MB
 total PGA used for auto workareas       0.0 MB
 total freeable PGA memory              24.3 MB

Top PGA 会话:

  ┌──────┬─────────────┬────────┬───────────────┬──────────────────────────┐
  │ SID  │ USER        │ PGA_MB │ SQL_ID        │ PROGRAM                  │
  ├──────┼─────────────┼────────┼───────────────┼──────────────────────────┤
  │ 1705 │ SYS         │ 3.0    │               │ oracle@oracledb01 (OFSD) │
  │ 431  │ OPENDB_TEST │ 2.9    │ frumv2gy7n85
```

---

## /sga

### 优化前
```
SGA 内存概览:

 组件                            大小          最小          最大
 ────────────────────────────────────────────────────────
 DEFAULT buffer cache    13120 MB    13120 MB    13120 MB
 shared pool              2048 MB     2048 MB     2048 MB
 large pool                512 MB      512 MB      512 MB
 java pool                 256 MB      256 MB      256 MB
 Shared IO Pool            128 MB      128 MB      128 MB
 ────────────────────────────────────────────────────────
 SGA Total               36864 MB

5 SGA components, total 36864 MB
```

### 优化后
```
SGA 内存概览:

  ┌──────────────────────┬──────────┬──────────┬──────────┐
  │ 组件                 │ 大小(MB) │ 最小(MB) │ 最大(MB) │
  ├──────────────────────┼──────────┼──────────┼──────────┤
  │ DEFAULT buffer cache │ 13120    │ 13120    │ 13120    │
  │ shared pool          │ 2048     │ 2048     │ 2048     │
  │ large pool           │ 512      │ 512      │ 512      │
  │ java pool            │ 256      │ 256      │ 256      │
  │ Shared IO Pool       │ 128      │ 128      │ 128      │
  └──────────────────────┴──────────┴──────────┴──────────┘


 SGA Total: 36864 MB

5 SGA components, total 36864 MB
```

---

## /redo

### 优化前
```
Redo 日志状态:

 GROUP    SIZE_MB  MEMBERS  STATUS     SEQUENCE
 ──────────────────────────────────────────────────
     1      500        3   INACTIVE   266
     2      500        3   INACTIVE   269
     3      500        3   CURRENT    270
     4      500        2   INACTIVE   267
     5      500        2   INACTIVE   268

最近24小时日志切换 (按小时):

 小时     切换次数
 ────────────────

归档目标:

 DEST                   STATUS   TARGET       ARCHIVER
 ──────────────────────────────────────────────────
 LOG_ARCHIVE_DEST_1     VALID    PRIMARY      FOREGROUND

5 redo groups, 0 switch hours, 1 archive dests
```

### 优化后
```
Redo 日志状态:

  ┌───────┬─────────┬─────────┬──────────┬──────────┐
  │ GROUP │ SIZE_MB │ MEMBERS │ STATUS   │ SEQUENCE │
  ├───────┼─────────┼─────────┼──────────┼──────────┤
  │ 1     │ 500     │ 3       │ INACTIVE │ 266      │
  │ 2     │ 500     │ 3       │ INACTIVE │ 269      │
  │ 3     │ 500     │ 3       │ CURRENT  │ 270      │
  │ 4     │ 500     │ 2       │ INACTIVE │ 267      │
  │ 5     │ 500     │ 2       │ INACTIVE │ 268      │
  └───────┴─────────┴─────────┴──────────┴──────────┘


最近24小时日志切换 (按小时):

最近24小时无日志切换


归档目标:

  ┌────────────────────┬────────┬─────────┬────────────┐
  │ DEST               │ STATUS │ TARGET  │ ARCHIVER   │
  ├────────────────────┼────────┼─────────┼────────────┤
  │ LOG_ARCHIVE_DEST_1 │ VALID  │ PRIMARY │ FOREGROUND │
  └────────────────────┴────────
```

---

## /sortusage

### 优化前
```
排序段使用:

 表空间                   总块数          已用块          空闲块      使用率
 ────────────────────────────────────────────────────────────
 TEMP                46848          512        46336     1.1%

排序段详情:

 USER             类型           段数         MB
 ─────────────────────────────────────────────
                  DATA          4        4.0

提示: /tempsess 查看对应会话

1 sort segments, 1 consumers
```

### 优化后
```
── 排序段使用 ──

  ┌─────────────────┬──────────────┬─────────────┬─────────────┬──────────┐
  │ TABLESPACE_NAME │ TOTAL_BLOCKS │ USED_BLOCKS │ FREE_BLOCKS │ USED_PCT │
  ├─────────────────┼──────────────┼─────────────┼─────────────┼──────────┤
  │ TEMP            │ 46848        │ 384         │ 46464       │ 0.8      │
  └─────────────────┴──────────────┴─────────────┴─────────────┴──────────┘

── 排序段详情 ──

  ┌──────────┬─────────┬──────────┬────┐
  │ USERNAME │ SEGTYPE │ SEGMENTS │ MB │
  ├──────────┼─────────┼──────────┼────┤
  │ N/A      │ DATA    │ 3        │ 3  │
  └──────────┴─────────┴──────────┴────┘

提示: /tempsess 查看对应会话

1 sort segments, 1 consumers
```

---

## /resource

### 优化前
```
资源限制使用:

 资源名                          当前       最大       上限      使用率
 ────────────────────────────────────────────────────────────
 enqueue_locks                31       49    27852     0.2%
 max_rollback_segments        27       27    65535     0.0%
 parallel_max_servers         32       32    32767     0.1%
 processes                   119      137     1500     9.1%
 sessions                    132      166     2272     7.3%

⚠ 无资源超过 80% 预警线
5 resources, 0 warnings
```

### 优化后
```
资源限制使用:

 资源名                          当前       最大       上限      使用率
 ────────────────────────────────────────────────────────────
 enqueue_locks                30       49    27852     0.2%
 max_rollback_segments        27       27    65535     0.0%
 parallel_max_servers         32       32    32767     0.1%
 processes                   119      137     1500     9.1%
 sessions                    132      166     2272     7.3%

✓ 所有资源使用率正常
5 resources, 0 warnings
```

---

## /os

### 优化前
```
OS 指标 (v$osstat):

  CPU:
    CPUs: 16  Cores: 8  Sockets: 1
    Busy: 0.1%  User: 0.1%  Sys: 0.0%  IO Wait: 0.0%

  Memory:
    Physical: 63239 MB  Used: 20219 MB (32.0%)  Free: 43020 MB

cpu_busy=0.1% mem_used=32.0%
```

### 优化后
```
OS 指标 (v$osstat):

  CPU:
    CPUs: 16  Cores: 8  Sockets: 1
    Busy: 0.1%  User: 0.1%  Sys: 0.0%  IO Wait: 0.0%

  Memory:
    Physical: 63239 MB  Used: 20209 MB (32.0%)  Free: 43030 MB

cpu_busy=0.1% mem_used=32.0%
```

---

## /os proc

### 优化前
```
┌─────┬──────────────────────────┬──────────────┬───────────────┬─────────────┐
  │ PID │ PROGRAM                  │ PGA_USED_MEM │ PGA_ALLOC_MEM │ PGA_MAX_MEM │
  ├─────┼──────────────────────────┼──────────────┼───────────────┼─────────────┤
  │ 99  │ oracle@oracledb01        │ 2032613      │ 2412277       │ 2412277     │
  │ 40  │ oracle@oracledb01 (D000) │ 1862445      │ 1945125       │ 1953525     │
  │ 43  │ oracle@oracledb01 (S000) │ 1178925      │ 1289765       │ 1298165     │
  │ 51  │ oracle@oracledb01 (P000) │ 1166085      │ 1224229       │ 1232629     │
  │ 57  │ oracle@oracledb01 (P006) │ 1166085      │ 1224229       │ 1232629     │
  │ 56  │ oracle@oracledb01 (P005) │ 1166085      │ 1224229       │ 1232629     │
  │ 55  │ oracle@oracledb01 (P004) │ 1166085      │ 1224229     
```

### 优化后
```
┌─────┬──────────────────────────┬─────────────┬──────────────┬────────────┐
  │ PID │ PROGRAM                  │ PGA_USED_MB │ PGA_ALLOC_MB │ PGA_MAX_MB │
  ├─────┼──────────────────────────┼─────────────┼──────────────┼────────────┤
  │ 99  │ oracle@oracledb01        │ 2.1         │ 2.7          │ 2.7        │
  │ 40  │ oracle@oracledb01 (D000) │ 1.8         │ 1.9          │ 1.9        │
  │ 43  │ oracle@oracledb01 (S000) │ 1.1         │ 1.2          │ 1.2        │
  │ 51  │ oracle@oracledb01 (P000) │ 1.1         │ 1.2          │ 1.2        │
  │ 57  │ oracle@oracledb01 (P006) │ 1.1         │ 1.2          │ 1.2        │
  │ 56  │ oracle@oracledb01 (P005) │ 1.1         │ 1.2          │ 1.2        │
  │ 55  │ oracle@oracledb01 (P004) │ 1.1         │ 1.2          │ 1.2        │
  │ 54  │ or
```

---

## /slowsql

### 优化前
```
┌───────────────┬────────────┬────────────┬────────────────┬─────────────┬───────────────────────────────────────────┐
  │ SQL_ID        │ ELAPSED_MS │ EXECUTIONS │ ROWS_PROCESSED │ BUFFER_GETS │ SQL_TEXT                                  │
  ├───────────────┼────────────┼────────────┼────────────────┼─────────────┼───────────────────────────────────────────┤
  │ 5368rwh8sxz77 │ 119222.12  │ 5264       │ 10528          │ 138         │ SELECT name, value FROM v$sysstat WHER... │
  │ 1mhd5z901v56z │ 11841.42   │ 522        │ 3132           │ 2           │ SELECT name, value FROM v$sysstat WHER... │
  │ 07hpyzy66cj9u │ 10222.77   │ 466        │ 1864           │ 2           │ SELECT name, value FROM v$sysstat WHER... │
  │ 23k121qmp73m3 │ 7961.86    │ 466        │ 4660           │ 188         │
```

### 优化后
```
┌───────────────┬────────────┬────────────┬────────────────┬─────────────┬───────────────────────────────────────────┐
  │ SQL_ID        │ ELAPSED_MS │ EXECUTIONS │ ROWS_PROCESSED │ BUFFER_GETS │ SQL_TEXT                                  │
  ├───────────────┼────────────┼────────────┼────────────────┼─────────────┼───────────────────────────────────────────┤
  │ 5368rwh8sxz77 │ 119222.12  │ 5264       │ 10528          │ 138         │ SELECT name, value FROM v$sysstat WHER... │
  │ 1mhd5z901v56z │ 11841.42   │ 522        │ 3132           │ 2           │ SELECT name, value FROM v$sysstat WHER... │
  │ 07hpyzy66cj9u │ 10222.77   │ 466        │ 1864           │ 2           │ SELECT name, value FROM v$sysstat WHER... │
  │ 23k121qmp73m3 │ 7961.86    │ 466        │ 4660           │ 188         │
```

---

## /topsql

### 优化前
```
┌─────────────┬───────────┬──────┬──────────────┬───────────┬────────────┬─────────────┬──────────────┬──────────────┐
  │ SQL_ID      │ ELAPSED_S │ EXEC │ AVG_ELAPS... │ LOGICAL_R │ PHYSICAL_R │ AVG_LOGICAL │ AVG_PHYSICAL │ SQL_TEXT     │
  ├─────────────┼───────────┼──────┼──────────────┼───────────┼────────────┼─────────────┼──────────────┼──────────────┤
  │ 22356bkg... │ 1.2       │ 57   │ 0.021        │ 17        │ 0          │ 0           │ 0            │ SELECT CO... │
  │ 1fvsn5j5... │ 0.8       │ 26   │ 0.03         │ 0         │ 0          │ 0           │ 0            │       beg... │
  │ aykvshm7... │ 0.8       │ 755  │ 0.001        │ 129       │ 6          │ 0           │ 0            │ select si... │
  │ 28bgqbzp... │ 0.6       │ 26   │ 0.022        │ 56        │ 2          │
```

### 优化后
```
┌───────────────┬───────────┬──────┬────────────────┬───────────────┬─────────────┬──────────────────────────────────┐
  │ SQL_ID        │ ELAPSED_S │ EXEC │ AVG_ELAPSED_MS │ LOGICAL_READS │ AVG_LOGICAL │ SQL_TEXT                         │
  ├───────────────┼───────────┼──────┼────────────────┼───────────────┼─────────────┼──────────────────────────────────┤
  │ 22356bkgsdcnh │ 1.2       │ 59   │ 0.021          │ 17            │ 0           │ SELECT COUNT(*) FROM X$KSPPI ... │
  │ 1fvsn5j51ugz3 │ 0.8       │ 27   │ 0.03           │ 0             │ 0           │       begin          dbms_rcv... │
  │ aykvshm7zsabd │ 0.8       │ 792  │ 0.001          │ 129           │ 0           │ select size_for_estimate,    ... │
  │ 28bgqbzpa87xf │ 0.6       │ 27   │ 0.022          │ 56            │ 2   
```

---

## /ash

### 优化前
```
ASH 分析 (最近 5 分钟)

── Top SQL (按采样数) ──
 SQL_ID           Samples   Pct%  Plan Hash     Last Seen
 ──────────────────────────────────────────────────────────────────────
 2f62s7jnadf6n          1   100%  3938847960    2026-03-18 15:37:37.32 +0000 UTC

── Top Wait Events (按采样数) ──
 Event                                    Wait Class          Samples   Pct%
 ──────────────────────────────────────────────────────────────────────────────
 ON CPU                                                             2   100%

ASH 分析 (最近 5 分钟) — 1 SQL, 1 等待事件
```

### 优化后
```
ASH 分析 (最近 5 分钟)

── Top SQL (按采样数) ──
 SQL_ID           Samples   Pct%  Plan Hash     Last Seen
 ──────────────────────────────────────────────────────────────────────
 0k90b5rp453yc          1   100%  4078266309    15:56:23

── Top Wait Events (按采样数) ──
 Event                                    Wait Class          Samples   Pct%
 ──────────────────────────────────────────────────────────────────────────────
 control file sequential read             System I/O                1    50%
 ON CPU                                                             1    50%

ASH 分析 (最近 5 分钟) — 1 SQL, 2 等待事件
```

---

## /ash 10

### 优化前
```
ASH 分析 (最近 10 分钟)

── Top SQL (按采样数) ──
 SQL_ID           Samples   Pct%  Plan Hash     Last Seen
 ──────────────────────────────────────────────────────────────────────
 2f62s7jnadf6n          1   100%  3938847960    2026-03-18 15:37:37.32 +0000 UTC

── Top Wait Events (按采样数) ──
 Event                                    Wait Class          Samples   Pct%
 ──────────────────────────────────────────────────────────────────────────────
 ON CPU                                                             9   100%

ASH 分析 (最近 10 分钟) — 1 SQL, 1 等待事件
```

### 优化后
```
ASH 分析 (最近 10 分钟)

── Top SQL (按采样数) ──
 SQL_ID           Samples   Pct%  Plan Hash     Last Seen
 ──────────────────────────────────────────────────────────────────────
 0k90b5rp453yc          1   100%  4078266309    15:56:23

── Top Wait Events (按采样数) ──
 Event                                    Wait Class          Samples   Pct%
 ──────────────────────────────────────────────────────────────────────────────
 ON CPU                                                             5  71.4%
 control file sequential read             System I/O                2  28.6%

ASH 分析 (最近 10 分钟) — 1 SQL, 2 等待事件
```

---

## /space

### 优化前
```
┌─────────────────┬─────────┬──────────┬──────────┐
  │ TABLESPACE_NAME │ USED_MB │ TOTAL_MB │ USED_PCT │
  ├─────────────────┼─────────┼──────────┼──────────┤
  │ USERS           │ 4233.19 │ 8192     │ 51.67    │
  │ SYSTEM          │ 938.5   │ 1842     │ 50.95    │
  │ SYSAUX          │ 726.5   │ 2430.11  │ 29.9     │
  │ TEMP            │ 4       │ 4834.23  │ 0.08     │
  └─────────────────┴─────────┴──────────┴──────────┘

4 tablespace(s)
```

### 优化后
```
┌─────────────────┬─────────┬──────────┬──────────┐
  │ TABLESPACE_NAME │ USED_MB │ TOTAL_MB │ USED_PCT │
  ├─────────────────┼─────────┼──────────┼──────────┤
  │ USERS           │ 4233.19 │ 8192     │ 51.67    │
  │ SYSTEM          │ 938.5   │ 1842     │ 50.95    │
  │ SYSAUX          │ 726.5   │ 2430.06  │ 29.9     │
  │ TEMP            │ 3       │ 4834.09  │ 0.06     │
  └─────────────────┴─────────┴──────────┴──────────┘

4 tablespace(s)
```

---

## /alert 24

### 优化前
```
┌──────────────────────────────────────┬─────────────────────────────────────────────────────────────┬───────────────┐
  │ ORIGINATING_TIMESTAMP                │ MESSAGE_TEXT                                                │ MESSAGE_LEVEL │
  ├──────────────────────────────────────┼─────────────────────────────────────────────────────────────┼───────────────┤
  │ 2026-03-18 15:20:36.961 +0800 +08:00 │ Incremental checkpoint up to RBA [0x10e.1e99.0], current... │ 16            │
  │ 2026-03-18 14:50:34.619 +0800 +08:00 │ Incremental checkpoint up to RBA [0x10e.208.0], current ... │ 16            │
  │ 2026-03-18 14:20:32.277 +0800 +08:00 │ Incremental checkpoint up to RBA [0x10e.99.0], current l... │ 16            │
  │ 2026-03-18 14:10:30.059 +0800 +08:00 │ Completed checkpoint up to RBA [0
```

### 优化后
```
┌────────────────┬───────────────────────────────────────────────────────────────────────────────────┬───────────────┐
  │ TIME           │ MESSAGE_TEXT                                                                      │ MESSAGE_LEVEL │
  ├────────────────┼───────────────────────────────────────────────────────────────────────────────────┼───────────────┤
  │ 03-18 15:50:39 │ Incremental checkpoint up to RBA [0x10e.207f.0], current log tail at RBA [0x10... │ 16            │
  │ 03-18 15:20:36 │ Incremental checkpoint up to RBA [0x10e.1e99.0], current log tail at RBA [0x10... │ 16            │
  │ 03-18 14:50:34 │ Incremental checkpoint up to RBA [0x10e.208.0], current log tail at RBA [0x10e... │ 16            │
  │ 03-18 14:20:32 │ Incremental checkpoint up to RBA [0x10e.99.0], current 
```

---

## /standby

### 优化前
```
=== Database Info ===
  DATABASE_ROLE:       PRIMARY
  DB_UNIQUE_NAME:      orcl
  PROTECTION_MODE:     MAXIMUM PERFORMANCE
  SWITCHOVER_STATUS:   NOT ALLOWED
  DATAGUARD_BROKER:    DISABLED

=== Archive Dest Status ===
DEST_NAME        STATUS           TYPE             SRL              GAP_STATUS     
LOG_ARCHIVE_DEST_1  VALID            LOCAL            NO               <nil>          

Data Guard status
```

### 优化后
```
── Database Info ──
  DATABASE_ROLE:       PRIMARY
  DB_UNIQUE_NAME:      orcl
  PROTECTION_MODE:     MAXIMUM PERFORMANCE
  SWITCHOVER_STATUS:   NOT ALLOWED
  DATAGUARD_BROKER:    DISABLED

── Archive Dest Status ──
  ┌────────────────────┬────────┬───────┬─────┬────────────┐
  │ DEST_NAME          │ STATUS │ TYPE  │ SRL │ GAP_STATUS │
  ├────────────────────┼────────┼───────┼─────┼────────────┤
  │ LOG_ARCHIVE_DEST_1 │ VALID  │ LOCAL │ NO  │ -          │
  └────────────────────┴────────┴───────┴─────┴────────────┘

Data Guard status
```

---

## /resize TEMP

### 优化前
```
表空间 TEMP 文件:

 #    FILE_NAME                                 SIZE_MB  AUTO    MAX_MB
 ──────────────────────────────────────────────────────────────────────
    1 /oracle/oradata/ORCL/datafile/o1_mf_temp_nsxnvlqm_.tmp      367  ON       32768
    2 /oracle/oradata/ORCL/datafile/o1_mf_temp_nsxnvlqm_02.tmp     2048  OFF          0
    3 /oracle/oradata/ORCL/datafile/o1_mf_temp_nsxnvlqm_03.tmp     2048  OFF          0

操作:
  /resize TEMP resize <#> <大小>     扩容文件
  /resize TEMP add <路径> <大小>     新增文件
  /resize TEMP autoextend <#> <最大>  开启自动扩展

tablespace TEMP: 3 file(s)
```

### 优化后
```
┌─────────┬──────────────────────────────────────────────────────────┬─────────┬────────────────┬────────┐
  │ FILE_ID │ FILE_NAME                                                │ SIZE_MB │ AUTOEXTENSIBLE │ MAX_MB │
  ├─────────┼──────────────────────────────────────────────────────────┼─────────┼────────────────┼────────┤
  │ 1       │ /oracle/oradata/ORCL/datafile/o1_mf_temp_nsxnvlqm_.tmp   │ 367     │ YES            │ 32768  │
  │ 2       │ /oracle/oradata/ORCL/datafile/o1_mf_temp_nsxnvlqm_02.tmp │ 2048    │ NO             │ 0      │
  │ 3       │ /oracle/oradata/ORCL/datafile/o1_mf_temp_nsxnvlqm_03.tmp │ 2048    │ NO             │ 0      │
  └─────────┴──────────────────────────────────────────────────────────┴─────────┴────────────────┴────────┘

表空间 TEMP: 3 file(s)

操作:
  /resize TEM
```

---

## /help

### 优化前
```
可用命令:

 [1;36m监控大盘:[0m
  /dbtop            实时性能监控面板
  /health           数据库健康检查

 [1;36m会话/锁:[0m
  /sessions         所有会话概览
  /activesessions   活跃会话列表
  /waits            等待事件统计
  /locks            行锁/表锁
  /blocktree        层级阻塞链 (阻塞者→被阻塞会话树)
  /latches          Latch 争用
  /mutexes          Mutex 争用

 [1;36mSQL 分析:[0m
  /slowsql          慢 SQL 查询 (默认 1000ms)
  /topsql           Top SQL (按执行时间/逻辑读等排序)
  /explain          SQL 执行计划
  /sql              执行自定义 SQL

 [1;36m内存/存储:[0m
  /sga              SGA 内存详情
  /pga              PGA 内存详情
  /space            表空间使用率
  /tempsess         临时空间占用会话
  /undosess         Undo 占用会话
  /sortusage        排序段使用详情
  /redo             Redo 日志状态
  /fra              FRA 使用详情
  /asm              ASM 磁盘组

 [1;36m管理:[0m
  /kill             终止会话
  /alter  
```

### 优化后
```
可用命令:

 [1;36m监控大盘:[0m
  /dbtop            实时性能监控面板
  /health           数据库健康检查

 [1;36m会话/锁:[0m
  /sessions         所有会话概览
  /activesessions   活跃会话列表
  /waits            等待事件统计
  /locks            行锁/表锁
  /blocktree        层级阻塞链 (阻塞者→被阻塞会话树)
  /latches          Latch 争用
  /mutexes          Mutex 争用

 [1;36mSQL 分析:[0m
  /slowsql          慢 SQL 查询 (默认 1000ms)
  /topsql           Top SQL (按执行时间/逻辑读等排序)
  /explain          SQL 执行计划
  /sql              执行自定义 SQL
  /awr              AWR 快照分析
  /ash              ASH 活跃会话采样分析
  /planhistory      SQL 执行计划历史 (检测计划回退)

 [1;36m内存/存储:[0m
  /sga              SGA 内存详情
  /pga              PGA 内存详情
  /space            表空间使用率
  /tempsess         临时空间占用会话
  /undosess         Undo 占用会话
  /sortusage        排序段使用详情
  /redo             Redo 日志状态
  /fra 
```

---

## 未变化的命令

- `/locks` — 输出无变化
- `/undosess` — 输出无变化
- `/fra` — 输出无变化
- `/asm` — 输出无变化
- `/blocktree` — 输出无变化
- `/segments` — 输出无变化
- `/segments OPENDB_TEST` — 输出无变化
- `/awr` — 输出无变化
- `/params pga` — 输出无变化
- `/backup` — 输出无变化
- `/jobs` — 输出无变化
- `/planhistory 11b9k2abc1234` — 输出无变化
