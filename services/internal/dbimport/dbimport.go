// Package dbimport knows how to load data into each database engine IGNITION runs: from a dump file,
// or straight from a live database. The scripts run inside the target database's own container (its
// official image ships the client tools and its credentials are in its environment), except for the
// Redis family, which runs redis-cli from a small helper container on the same private network.
package dbimport

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Engine struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	DefaultPort int      `json:"defaultPort"`
	Scheme      []string `json:"scheme"`  // connection URL schemes
	Formats     []string `json:"formats"` // what a dump file can be, in words
	Extensions  []string `json:"extensions"`
	Live        string   `json:"live"`    // how a live migration works, in words
	Replace     string   `json:"replace"` // what "replace existing data" does
	Helper      bool     `json:"-"`       // runs from a redis-cli helper container
	Ready       string   `json:"-"`       // exits 0 once the database accepts connections
}

var Engines = []Engine{
	{ID: "postgresql", Label: "PostgreSQL", DefaultPort: 5432, Scheme: []string{"postgres", "postgresql"},
		Formats: []string{"SQL dump (pg_dump, plain)", "Custom-format dump (pg_dump -Fc)", "Either one gzipped"}, Extensions: []string{".sql", ".dump", ".backup", ".gz"},
		Live:    "Copies the source with pg_dump into this database. The target's pg_dump must be at least the source's major version.",
		Replace: "Drops and recreates the public schema first.",
		Ready:   `pg_isready -q -U "${POSTGRES_USER:-postgres}"`},
	{ID: "mysql", Label: "MySQL", DefaultPort: 3306, Scheme: []string{"mysql"},
		Formats: []string{"SQL dump (mysqldump)", "Gzipped SQL dump"}, Extensions: []string{".sql", ".gz"},
		Live:    "Copies the source database with mysqldump (single transaction, with routines and triggers).",
		Replace: "Drops and recreates the database first.",
		Ready:   `mysqladmin ping --silent -uroot -p"$MYSQL_ROOT_PASSWORD" 2>/dev/null || mysqladmin ping --silent -u"$MYSQL_USER" -p"$MYSQL_PASSWORD"`},
	{ID: "mariadb", Label: "MariaDB", DefaultPort: 3306, Scheme: []string{"mysql", "mariadb"},
		Formats: []string{"SQL dump (mariadb-dump or mysqldump)", "Gzipped SQL dump"}, Extensions: []string{".sql", ".gz"},
		Live:    "Copies the source database with mariadb-dump (single transaction, with routines and triggers).",
		Replace: "Drops and recreates the database first.",
		Ready:   `(command -v healthcheck.sh >/dev/null && healthcheck.sh --connect) || mariadb-admin ping --silent -uroot -p"$MARIADB_ROOT_PASSWORD"`},
	{ID: "mongodb", Label: "MongoDB", DefaultPort: 27017, Scheme: []string{"mongodb", "mongodb+srv"},
		Formats: []string{"Archive (mongodump --archive)", "Gzipped archive (mongodump --archive --gzip)"}, Extensions: []string{".archive", ".gz", ".dump"},
		Live:    "Copies the source with mongodump and restores it here.",
		Replace: "Drops each collection before restoring it (mongorestore --drop).",
		Ready:   `mongosh --quiet -u "$MONGO_INITDB_ROOT_USERNAME" -p "$MONGO_INITDB_ROOT_PASSWORD" --authenticationDatabase admin --eval 'db.runCommand({ping:1}).ok' | grep -q 1`},
	{ID: "redis", Label: "Redis", DefaultPort: 6379, Scheme: []string{"redis"}, Helper: true,
		Formats: []string{"Commands file (one command per line, or RESP from redis-cli --pipe)", "Gzipped commands file"}, Extensions: []string{".txt", ".redis", ".resp", ".gz"},
		Live:    "Replicates from the source (REPLICAOF) until it's in sync, then stops replicating. The source must be reachable from this machine; TLS sources (rediss://) aren't supported.",
		Replace: "Flushes all keys first (a live migration always replaces)."},
	{ID: "dragonfly", Label: "Dragonfly", DefaultPort: 6379, Scheme: []string{"redis", "dragonfly"}, Helper: true,
		Formats: []string{"Commands file (one command per line, or RESP)", "Gzipped commands file"}, Extensions: []string{".txt", ".redis", ".resp", ".gz"},
		Live:    "Replicates from a Redis or Dragonfly source (REPLICAOF) until it's in sync, then stops replicating.",
		Replace: "Flushes all keys first (a live migration always replaces)."},
	{ID: "clickhouse", Label: "ClickHouse", DefaultPort: 9000, Scheme: []string{"clickhouse", "tcp"},
		Formats: []string{"SQL file (CREATE and INSERT statements)", "Gzipped SQL file"}, Extensions: []string{".sql", ".gz"},
		Live:    "Recreates each table of the source database here and copies its rows with remote() over the native protocol (port 9000).",
		Replace: "Drops the tables it's about to import first.",
		Ready:   `clickhouse-client --user "${CLICKHOUSE_USER:-default}" --password "${CLICKHOUSE_PASSWORD}" -q 'SELECT 1'`},
}

func EngineByID(id string) *Engine {
	for i := range Engines {
		if Engines[i].ID == id {
			return &Engines[i]
		}
	}
	return nil
}

// Source is a parsed live connection URL.
type Source struct {
	Host, User, Password, Database string
	Port                           int
	TLS                            bool
	URL                            string // as given (for tools that take a URL)
}

// ParseURL reads a connection URL for an engine and says clearly what's wrong with it.
func ParseURL(e *Engine, raw string) (*Source, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("enter a URL like %s://user:password@host:%d/database", e.Scheme[0], e.DefaultPort)
	}
	scheme := strings.ToLower(u.Scheme)
	ok, tlsScheme := false, false
	for _, s := range e.Scheme {
		ok = ok || s == scheme || s+"s" == scheme
		tlsScheme = tlsScheme || (s+"s" == scheme && !contains(e.Scheme, scheme))
	}
	if !ok {
		return nil, fmt.Errorf("a %s URL starts with %s://", e.Label, e.Scheme[0])
	}
	s := &Source{Host: u.Hostname(), User: u.User.Username(), Database: strings.TrimPrefix(u.Path, "/"), Port: e.DefaultPort, URL: raw}
	s.Password, _ = u.User.Password()
	s.TLS = tlsScheme || u.Query().Get("sslmode") == "require" || u.Query().Get("ssl") == "true" || u.Query().Get("tls") == "true"
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("the port isn't a number from 1 to 65535")
		}
		s.Port = n
	}
	if e.Helper && s.TLS {
		return nil, fmt.Errorf("TLS sources (rediss://) can't be replicated from; use a plain redis:// address reachable from this machine")
	}
	if e.Helper && strings.Contains(s.Database, "/") {
		return nil, fmt.Errorf("leave out the path, or give just a database number")
	}
	if (e.ID == "mysql" || e.ID == "mariadb" || e.ID == "clickhouse") && s.Database == "" {
		return nil, fmt.Errorf("add the database name to the URL: …:%d/database", s.Port)
	}
	if s.Host == "localhost" || s.Host == "127.0.0.1" || s.Host == "::1" {
		return nil, fmt.Errorf("localhost means the database's own container here; use the source's network address (or host.docker.internal for this machine)")
	}
	return s, nil
}

// Address is host:port for display and dialling.
func (s *Source) Address() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// Masked is the URL without its password, for logs and history.
func Masked(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	q := u.Query() // keep options like authSource; drop anything that carries a secret
	for k := range q {
		if l := strings.ToLower(k); strings.Contains(l, "pass") || strings.Contains(l, "secret") || strings.Contains(l, "token") || strings.Contains(l, "key") {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Env passes a source to a script without putting secrets on a command line.
func (s *Source) Env() []string {
	return []string{"SRC_URL=" + s.URL, "SRC_HOST=" + s.Host, "SRC_PORT=" + strconv.Itoa(s.Port), "SRC_USER=" + s.User, "SRC_PASS=" + s.Password, "SRC_DB=" + s.Database}
}

const prelude = "set -e\nsay() { printf '==> %s\\n' \"$1\"; }\n"

// FileScript restores the file at $F (inside the target container) into the database.
func FileScript(e *Engine) string {
	switch e.ID {
	case "postgresql":
		return prelude + `export PGPASSWORD="$POSTGRES_PASSWORD"; U="${POSTGRES_USER:-postgres}"; D="${POSTGRES_DB:-$U}"
if gzip -t "$F" 2>/dev/null; then say "Unpacking gzip"; gunzip -c "$F" > "$F.raw"; F="$F.raw"; fi
if [ "$REPLACE" = 1 ]; then say "Replacing: dropping schema public"; psql -q -v ON_ERROR_STOP=1 -U "$U" -d "$D" -c 'DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;'; fi
if [ "$(head -c 5 "$F")" = "PGDMP" ]; then
  say "Restoring a custom-format dump into $D with pg_restore"
  pg_restore --no-owner --no-privileges --exit-on-error -U "$U" -d "$D" "$F"
else
  say "Running the SQL dump against $D"
  psql -q -v ON_ERROR_STOP=1 -U "$U" -d "$D" -f "$F"
fi
say "Tables now: $(psql -tA -U "$U" -d "$D" -c "SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')")"
`
	case "mysql", "mariadb":
		return prelude + mysqlCreds + `if gzip -t "$F" 2>/dev/null; then say "Unpacking gzip"; gunzip -c "$F" > "$F.raw"; F="$F.raw"; fi
if [ "$REPLACE" = 1 ]; then say "Replacing: recreating database $DB"; $CLI $AUTH -e "DROP DATABASE IF EXISTS ` + "\\`$DB\\`" + `; CREATE DATABASE ` + "\\`$DB\\`" + `;"; fi
say "Running the SQL dump against $DB"
$CLI $AUTH "$DB" < "$F"
say "Tables now: $($CLI $AUTH -N -e "SELECT count(*) FROM information_schema.tables WHERE table_schema='$DB'")"
`
	case "mongodb":
		return prelude + `AUTH="-u $MONGO_INITDB_ROOT_USERNAME -p $MONGO_INITDB_ROOT_PASSWORD --authenticationDatabase admin"
GZ=""; if gzip -t "$F" 2>/dev/null; then GZ="--gzip"; fi
DROP=""; if [ "$REPLACE" = 1 ]; then DROP="--drop"; fi
say "Restoring the archive with mongorestore $GZ $DROP"
mongorestore $AUTH --archive="$F" $GZ $DROP --stopOnError
`
	case "clickhouse":
		return prelude + clickhouseCreds + `if gzip -t "$F" 2>/dev/null; then say "Unpacking gzip"; gunzip -c "$F" > "$F.raw"; F="$F.raw"; fi
if [ "$REPLACE" = 1 ]; then say "Replacing: dropping tables in $DB that the file creates"
  grep -ioE 'CREATE TABLE (IF NOT EXISTS )?[` + "`" + `"A-Za-z0-9_.]+' "$F" | awk '{print $NF}' | tr -d '` + "`" + `"' | sed "s/^.*\.//" | sort -u | while read t; do $CH -q "DROP TABLE IF EXISTS $DB.$t"; done
fi
say "Running the SQL file against $DB"
$CH -d "$DB" --multiquery < "$F"
say "Tables now: $($CH -q "SELECT count() FROM system.tables WHERE database = '$DB'")"
`
	case "redis", "dragonfly":
		return prelude + `if gzip -t "$F" 2>/dev/null; then say "Unpacking gzip"; gunzip -c "$F" > "$F.raw"; F="$F.raw"; fi
if [ "$REPLACE" = 1 ]; then say "Replacing: FLUSHALL"; redis-cli -h "$TARGET" -p 6379 $AUTH FLUSHALL >/dev/null; fi
say "Sending the commands"
redis-cli -h "$TARGET" -p 6379 $AUTH --pipe < "$F"
say "Keys now: $(redis-cli -h "$TARGET" -p 6379 $AUTH DBSIZE)"
`
	}
	return ""
}

const mysqlCreds = `CLI=$(command -v mariadb || command -v mysql); DUMP=$(command -v mariadb-dump || command -v mysqldump)
DB="${MARIADB_DATABASE:-$MYSQL_DATABASE}"
if [ -n "${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}" ]; then export MYSQL_PWD="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"; AUTH="-uroot"
else export MYSQL_PWD="${MARIADB_PASSWORD:-$MYSQL_PASSWORD}"; AUTH="-u${MARIADB_USER:-$MYSQL_USER}"; fi
if [ -z "$DB" ]; then echo "This database has no MYSQL_DATABASE / MARIADB_DATABASE set, so there's no database to import into."; exit 3; fi
`

const clickhouseCreds = `CH="clickhouse-client --user ${CLICKHOUSE_USER:-default} --password ${CLICKHOUSE_PASSWORD}"; DB="${CLICKHOUSE_DB:-default}"
`

// LiveScript copies from the source described by SRC_* variables.
func LiveScript(e *Engine) string {
	switch e.ID {
	case "postgresql":
		return prelude + `export PGPASSWORD="$POSTGRES_PASSWORD"; U="${POSTGRES_USER:-postgres}"; D="${POSTGRES_DB:-$U}"
say "Source: $(pg_dump --version)"
say "Dumping from $SRC_HOST:$SRC_PORT"
PGPASSWORD="$SRC_PASS" pg_dump --no-owner --no-privileges -h "$SRC_HOST" -p "$SRC_PORT" -U "$SRC_USER" -d "${SRC_DB:-postgres}" -Fc -f /tmp/ignition-live.dump
if [ "$REPLACE" = 1 ]; then say "Replacing: dropping schema public"; psql -q -v ON_ERROR_STOP=1 -U "$U" -d "$D" -c 'DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;'; fi
say "Restoring into $D"
pg_restore --no-owner --no-privileges --exit-on-error -U "$U" -d "$D" /tmp/ignition-live.dump
rm -f /tmp/ignition-live.dump
say "Tables now: $(psql -tA -U "$U" -d "$D" -c "SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')")"
`
	case "mysql", "mariadb":
		return prelude + mysqlCreds + `say "Dumping $SRC_DB from $SRC_HOST:$SRC_PORT"
MYSQL_PWD="$SRC_PASS" $DUMP -h "$SRC_HOST" -P "$SRC_PORT" -u "$SRC_USER" --single-transaction --routines --triggers --no-tablespaces --set-gtid-purged=OFF "$SRC_DB" > /tmp/ignition-live.sql 2>/tmp/ignition-live.err || \
  MYSQL_PWD="$SRC_PASS" $DUMP -h "$SRC_HOST" -P "$SRC_PORT" -u "$SRC_USER" --single-transaction --routines --triggers --no-tablespaces "$SRC_DB" > /tmp/ignition-live.sql
if [ "$REPLACE" = 1 ]; then say "Replacing: recreating database $DB"; $CLI $AUTH -e "DROP DATABASE IF EXISTS ` + "\\`$DB\\`" + `; CREATE DATABASE ` + "\\`$DB\\`" + `;"; fi
say "Restoring into $DB"
$CLI $AUTH "$DB" < /tmp/ignition-live.sql
rm -f /tmp/ignition-live.sql /tmp/ignition-live.err
say "Tables now: $($CLI $AUTH -N -e "SELECT count(*) FROM information_schema.tables WHERE table_schema='$DB'")"
`
	case "mongodb":
		return prelude + `AUTH="-u $MONGO_INITDB_ROOT_USERNAME -p $MONGO_INITDB_ROOT_PASSWORD --authenticationDatabase admin"
DROP=""; if [ "$REPLACE" = 1 ]; then DROP="--drop"; fi
say "Dumping from the source"
mongodump --uri="$SRC_URL" --archive=/tmp/ignition-live.archive --gzip
say "Restoring"
mongorestore $AUTH --archive=/tmp/ignition-live.archive --gzip $DROP --stopOnError
rm -f /tmp/ignition-live.archive
`
	case "clickhouse":
		return prelude + clickhouseCreds + `SRC="clickhouse-client --host $SRC_HOST --port $SRC_PORT --user ${SRC_USER:-default} --password $SRC_PASS"
TABLES=$($SRC -q "SELECT name FROM system.tables WHERE database = '$SRC_DB' AND NOT is_temporary AND engine NOT LIKE '%View'")
[ -n "$TABLES" ] || { echo "The source database $SRC_DB has no tables."; exit 3; }
for t in $TABLES; do
  say "Table $t"
  [ "$REPLACE" = 1 ] && $CH -q "DROP TABLE IF EXISTS $DB.$t"
  $SRC -q "SHOW CREATE TABLE $SRC_DB.$t FORMAT TSVRaw" | sed "s/^CREATE TABLE $SRC_DB\./CREATE TABLE IF NOT EXISTS $DB./" | $CH --multiquery
  $CH -q "INSERT INTO $DB.$t SELECT * FROM remote('$SRC_HOST:$SRC_PORT', '$SRC_DB', '$t', '${SRC_USER:-default}', '$SRC_PASS')"
  echo "  $($CH -q "SELECT count() FROM $DB.$t") rows"
done
`
	case "redis", "dragonfly":
		return prelude + `R="redis-cli -h $TARGET -p 6379 $AUTH"
S="redis-cli -h $SRC_HOST -p $SRC_PORT"
WANT=$(REDISCLI_AUTH="$SRC_PASS" $S --no-auth-warning DBSIZE 2>/dev/null | tr -dc 0-9)
[ -n "$WANT" ] && say "The source has $WANT keys"
say "Replicating from $SRC_HOST:$SRC_PORT"
if [ -n "$SRC_PASS" ]; then $R CONFIG SET masterauth "$SRC_PASS" >/dev/null || echo "Couldn't set masterauth; the source may refuse the connection."; fi
if [ -n "$SRC_USER" ] && [ "$SRC_USER" != default ]; then $R CONFIG SET masteruser "$SRC_USER" >/dev/null || true; fi
$R REPLICAOF "$SRC_HOST" "$SRC_PORT" >/dev/null
sleep 2
i=0; HAVE=0
while [ $i -lt 900 ]; do
  INFO=$($R INFO replication | tr -d '\r')
  HAVE=$($R DBSIZE | tr -dc 0-9)
  case "$INFO" in *"master_link_status:up"*)
    case "$INFO" in *"master_sync_in_progress:1"*) ;; *)
      # Linked and not syncing: done once the keys are here (or the source's count is unknown).
      if [ -z "$WANT" ] || [ "${HAVE:-0}" -ge "$WANT" ]; then break; fi;;
    esac;;
  esac
  [ $((i % 10)) = 0 ] && echo "  syncing: ${HAVE:-0}${WANT:+ of $WANT} keys ($i s)"
  i=$((i+1)); sleep 1
done
$R REPLICAOF NO ONE >/dev/null
$R CONFIG SET masterauth "" >/dev/null 2>&1 || true
case "$INFO" in *"master_link_status:up"*) ;; *) echo "The source never answered as a replication source within 15 minutes:"; echo "$INFO" | grep -E 'master_link|last_io|role'; exit 4;; esac
HAVE=$($R DBSIZE | tr -dc 0-9)
if [ -n "$WANT" ] && [ "$WANT" -gt 0 ] && [ "${HAVE:-0}" -lt "$WANT" ]; then echo "Replication stopped with ${HAVE:-0} of $WANT keys. The target may not support replicating from this source."; exit 5; fi
say "In sync; replication stopped. Keys now: $HAVE"
`
	}
	return ""
}

// HelperImage runs redis-cli for the Redis family.
const HelperImage = "redis:7.4-alpine"

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
