set -e
if [ -z "$DATABASE_URL" ] && [ "$EMBEDDED_POSTGRES" = "1" ]; then
  PGBIN=$(ls -d /usr/lib/postgresql/*/bin | head -n 1)
  export PGDATA=/tmp/pgdata
  if [ ! -s "$PGDATA/PG_VERSION" ]; then
    "$PGBIN/initdb" -D "$PGDATA" -U crosstown --auth=trust > /tmp/initdb.log
  fi
  "$PGBIN/pg_ctl" -D "$PGDATA" -o "-k /tmp -c listen_addresses=localhost -p 5432" -l /tmp/postgres.log -w start > /dev/null
  export DATABASE_URL="postgres://crosstown@localhost:5432/postgres?sslmode=disable"
fi
exec /app/crosstown -config /app/config.toml
