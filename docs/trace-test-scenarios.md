# Trace 功能测试场景：源码级堵塞分析

测试目标：制造数据库引擎内核级瓶颈，用 OpenDB 的 `/trace` + LLM 从源码层面分析堵塞并给出优化建议。

测试服务器：`ssh -p 2222 root@47.251.30.180`

## 前置条件

```bash
# 确认 perf 可用
perf --version

# 确认 opendb 在 PATH 中
which opendb
```

---

## 场景一：MySQL Redo Log 单线程写入瓶颈

### 源码缺陷

MySQL 5.7 / 8.0 早期版本中，redo log 写入由单一 `log_sys->mutex` 保护，所有并发写事务在 `log_write_up_to()` 排队等 flush，形成全局串行化。8.0.22+ 才引入 dedicated log writer thread 缓解。

### 火焰图预期特征

```
log_write_up_to() → log_sys->mutex → os_event_wait() → futex
```

### 准备

```sql
CREATE DATABASE IF NOT EXISTS tracetest;
USE tracetest;
CREATE TABLE redo_pressure (
    id INT AUTO_INCREMENT PRIMARY KEY,
    payload VARCHAR(500)
) ENGINE=InnoDB;
```

### 制造堵塞

```bash
# 方案 A：sysbench（推荐）
sysbench oltp_write_only \
  --mysql-host=127.0.0.1 --mysql-user=root --mysql-password=xxx \
  --mysql-db=tracetest --table-size=100000 --tables=4 \
  --threads=64 --time=30 prepare

sysbench oltp_write_only \
  --mysql-host=127.0.0.1 --mysql-user=root --mysql-password=xxx \
  --mysql-db=tracetest --table-size=100000 --tables=4 \
  --threads=64 --time=30 run

# 方案 B：纯 bash（无需 sysbench）
for i in $(seq 1 64); do
  mysql -u root -p tracetest -e "
    INSERT INTO redo_pressure(payload) VALUES (REPEAT('x', 500));
    INSERT INTO redo_pressure(payload) VALUES (REPEAT('y', 500));
    INSERT INTO redo_pressure(payload) VALUES (REPEAT('z', 500));
  " &
done
wait
```

### LLM 预期分析方向

- `log_write_up_to()` 中 `log_sys->mutex` 是全局瓶颈，所有 commit 串行等待
- 调用链：`trx_commit() → trx_flush_log_if_needed() → log_write_up_to() → mutex spin`
- 优化建议：升级到 8.0.22+（parallel log writer）、调大 `innodb_log_buffer_size`、`innodb_flush_log_at_trx_commit=2`（业务允许时）

---

## 场景二：PostgreSQL WAL Insert Lock 硬编码瓶颈

### 源码缺陷

PostgreSQL 的 WAL 写入使用 `NUM_XLOGINSERT_LOCKS`（`src/backend/access/transam/xlog.c` 中硬编码为 8），高并发写入时 8 把锁不够分，所有 backend 在 `WALInsertLockAcquire()` 排队。这是无法通过配置调优的编译期常量。

### 火焰图预期特征

```
XLogInsertRecord() → WALInsertLockAcquire() → LWLockAcquire() → sem_timedwait → futex
```

### 准备

```sql
CREATE DATABASE tracetest;
\c tracetest

CREATE TABLE wal_pressure (
    id SERIAL PRIMARY KEY,
    payload TEXT
);
```

### 制造堵塞

```bash
# 方案 A：pgbench（推荐，PG 自带）
pgbench -i -s 10 tracetest
pgbench -c 64 -j 8 -T 30 -P 5 tracetest

# 方案 B：纯 bash
for i in $(seq 1 64); do
  psql -d tracetest -c "
    INSERT INTO wal_pressure(payload)
    SELECT repeat('x', 500) FROM generate_series(1, 1000);
  " &
done
wait
```

### LLM 预期分析方向

- `xlog.c` 中 `NUM_XLOGINSERT_LOCKS = 8` 是硬编码常量，无法通过 GUC 参数调整
- 调用链：`XLogInsertRecord() → WALInsertLockAcquire() → LWLockAcquireOrWait()`，64 个 backend 争 8 把锁
- 优化建议：重新编译 PG 调大该常量（如 32/64）、减少单次 WAL 记录大小、`synchronous_commit=off`（业务允许时）

---

## 验证流程

1. SSH 到测试服务器
2. 分别为 MySQL / PG 制造上述压力（保持压力不停）
3. 另开终端，用 OpenDB 连接对应数据库
4. 输入：**"从源码层面分析当前堵塞"**
5. 验证 LLM 行为：
   - 调用 `locks` / `blocktree` / `sessions` / `waits` 定位堵塞
   - 调用 `trace` 采集 perf 数据，生成火焰图
   - 从 GitHub 源码查找热点函数（如 `log_write_up_to`、`WALInsertLockAcquire`）
   - 聊天输出源码级分析 + 火焰图 SVG 路径
6. 检查 `~/.opendb/trace/` 下 SVG 文件可用
