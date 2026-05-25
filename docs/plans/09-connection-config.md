# 连接管理与配置三库对比

## 1. 连接参数

| 参数 | Oracle | MySQL | PostgreSQL |
|------|--------|-------|-----------|
| 默认端口 | 1521 | 3306 | 5432 |
| 服务标识 | Service Name 或 SID | Database Name | Database Name |
| 连接串格式 | `oracle://user:pass@host:1521/service` | `user:pass@tcp(host:3306)/dbname` | `postgres://user:pass@host:5432/dbname` |
| Go 驱动 | `github.com/sijms/go-ora/v2` (纯 Go) | `github.com/go-sql-driver/mysql` (纯 Go) | `github.com/jackc/pgx/v5` (纯 Go) |
| 特权模式 | SYSDBA / SYSOPER | - (MySQL 无特权连接模式) | - (PG 用 superuser role 替代) |
| SSL/TLS | Oracle Wallet | `tls=true` / `ssl-mode=REQUIRED` | `sslmode=require/verify-ca/verify-full` |

## 2. 认证方式

| 方式 | Oracle | MySQL | PostgreSQL |
|------|--------|-------|-----------|
| 密码 | ✓ | ✓ | ✓ |
| OS 认证 | ✓ (AUTH TYPE=OS) | ✗ (MySQL 无 OS 认证) | ✓ (peer auth, 仅 Unix socket) |
| Wallet | ✓ (Oracle Wallet) | ✗ | ✗ |
| LDAP | ✓ | ✓ (auth_ldap_simple plugin) | ✓ (ldap auth method) |
| Kerberos | ✓ | ✓ (auth_kerberos plugin) | ✓ (gss auth method) |
| Token/OAuth | ✓ (自定义) | ✗ (原生不支持) | ✗ (原生不支持) |
| 证书 | ✓ (Wallet) | ✓ (client cert) | ✓ (cert auth method) |

## 3. ConnectionConfig 改造

### 当前结构
```go
type ConnectionConfig struct {
    DBType    string  // 已有但未用
    Host      string
    Port      int
    User      string
    Password  string
    Service   string  // Oracle 专用
    Database  string  // 通用
    Privilege string  // Oracle 专用 (sysdba/sysoper)
    Options   map[string]string
}
```

### 改造后（各库共用字段 + Options 存差异）
```go
type ConnectionConfig struct {
    DBType   string            // oracle / mysql / postgres
    Host     string
    Port     int
    User     string
    Password string
    Database string            // MySQL/PG 用此字段
    Service  string            // Oracle 专用 (Service Name)
    Options  map[string]string // 各库特有参数
}
```

### 各库 Options 差异

| Option Key | Oracle | MySQL | PostgreSQL |
|-----------|--------|-------|-----------|
| privilege | sysdba/sysoper | - | - |
| AUTH TYPE | OS/KERBEROS | - | - |
| WALLET | wallet_dir | - | - |
| charset | - | utf8mb4 | - |
| collation | - | utf8mb4_unicode_ci | - |
| ssl-mode | - | REQUIRED/VERIFY_CA | - |
| sslmode | - | - | require/verify-ca/verify-full |
| search_path | - | - | public,myschema |
| timezone | - | 时区设置 | 时区设置 |
| application_name | - | - | opendb |

## 4. Connection YAML 格式

### Oracle (当前)
```yaml
group: production
connections:
  - name: prod-oracle
    host: 10.0.1.1
    port: 1521
    service: orcl
    user: dba_admin
    auth_mode: save
    privilege: sysdba
```

### MySQL (新增)
```yaml
group: production
connections:
  - name: prod-mysql
    host: 10.0.2.1
    port: 3306
    db_type: mysql
    database: myapp
    user: dba_admin
    auth_mode: save
    options:
      charset: utf8mb4
      ssl-mode: REQUIRED
```

### PostgreSQL (新增)
```yaml
group: production
connections:
  - name: prod-pg
    host: 10.0.3.1
    port: 5432
    db_type: postgres
    database: myapp
    user: dba_admin
    auth_mode: save
    options:
      sslmode: require
      search_path: public,myschema
```

## 5. Setup Wizard 改造

### 当前 (wizard.go)
```
Database type: Oracle (auto-selected)
```

### 改造后
```
Database type:
  1) Oracle
  2) MySQL
  3) PostgreSQL
Select [1-3]: _
```

然后根据选择：
- Oracle → 提示 Host, Port(1521), Service/SID, User, Privilege
- MySQL → 提示 Host, Port(3306), Database, User, Charset
- PostgreSQL → 提示 Host, Port(5432), Database, User, SSLMode

## 6. /conn 连接串解析

### Oracle (当前)
```
/conn admin/pass@10.0.1.1:1521/orcl as sysdba
```

### MySQL
```
/conn admin/pass@10.0.2.1:3306/myapp
/conn admin@10.0.2.1/myapp     # 省略端口和密码
```

### PostgreSQL
```
/conn admin/pass@10.0.3.1:5432/myapp
/conn admin@10.0.3.1/myapp     # 省略端口和密码
```

### 自动检测数据库类型
根据端口号自动推测：
- 1521 → Oracle
- 3306 → MySQL
- 5432 → PostgreSQL
- 其他 → 让用户指定 `--type mysql`

## 7. DriverFactory 路由

### 当前 (main.go)
```go
connMgr, err := connection.NewManager(cfg,
    connection.WithDriverFactory(oracleDriverFactory),
)
```

### 改造后
```go
connMgr, err := connection.NewManager(cfg,
    connection.WithDriverFactory("oracle", oracleDriverFactory),
    connection.WithDriverFactory("mysql", mysqlDriverFactory),
    connection.WithDriverFactory("postgres", postgresDriverFactory),
)
```

Manager.Connect 根据 `Connection.DBType` 选择对应工厂。

## 8. ServerInfo 差异

### 当前 (连接时查询)
```go
type ServerInfo struct {
    Version      string
    InstanceName string
    Hostname     string
    Platform     string
    StartupTime  string
    CPUCores     int
    MemoryGB     float64
}
```

| 字段 | Oracle 来源 | MySQL 来源 | PG 来源 |
|------|------------|-----------|---------|
| Version | `v$instance.version_full` | `SELECT VERSION()` | `SELECT version()` |
| InstanceName | `v$instance.instance_name` | `SELECT @@hostname` | `SELECT current_database()` |
| Hostname | `v$instance.host_name` | `SELECT @@hostname` | `SELECT inet_server_addr()` |
| Platform | `v$database.platform_name` | `SELECT @@version_compile_os` | `SELECT version()` 解析 |
| StartupTime | `v$instance.startup_time` | `SELECT FROM_UNIXTIME(UNIX_TIMESTAMP() - VARIABLE_VALUE) FROM global_status WHERE VARIABLE_NAME='Uptime'` | `SELECT pg_postmaster_start_time()` |
| CPUCores | `V$OSSTAT NUM_CPU_CORES` | 无 SQL 获取（读 /proc/cpuinfo） | 无 SQL 获取（读 OS） |
| MemoryGB | `V$OSSTAT PHYSICAL_MEMORY_BYTES` | 无 SQL 获取 | 无 SQL 获取 |
