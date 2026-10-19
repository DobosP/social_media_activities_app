umask 077
pg=/usr/lib/postgresql/16/bin
data=/var/lib/postgresql/data
started=0
pid=0
cleanup() {
  status=$?
  trap - EXIT INT TERM
  set +e
  stop_rc=99; state_rc=99; pid_rc=99; files_absent=0
  if (( started )); then
    if [[ -f "$data/postmaster.pid" ]]; then read -r pid <"$data/postmaster.pid"; fi
    "$pg/pg_ctl" -D "$data" -m fast -w -t 20 stop >>/tmp/postgres.log 2>&1
    stop_rc=$?
    "$pg/pg_ctl" -D "$data" status >>/tmp/postgres.log 2>&1
    state_rc=$?
    if [[ $pid =~ ^[1-9][0-9]*$ ]]; then kill -0 "$pid" 2>/dev/null; pid_rc=$?; fi
    if [[ ! -e "$data/postmaster.pid" && ! -e /socket/.s.PGSQL.55434 && ! -e /socket/.s.PGSQL.55434.lock ]]; then files_absent=1; fi
  fi
  printf '{"stop_exit":%s,"status_exit":%s,"owned_pid":%s,"pid_probe_exit":%s,"pid_and_socket_files_absent":%s}\n' "$stop_rc" "$state_rc" "$pid" "$pid_rc" "$files_absent" >/socket/pg-cleanup.json
  chmod 0644 /socket/pg-cleanup.json
  tail -c 1048576 /tmp/postgres.log 2>/dev/null
  if (( stop_rc != 0 || state_rc != 3 || pid_rc != 1 || files_absent != 1 )); then status=1; fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
"$pg/initdb" -D "$data" --username=app --encoding=UTF8 --no-locale --auth-local=trust --auth-host=reject
init_rc=$?
if (( init_rc != 0 )); then exit "$init_rc"; fi
cat >>"$data/postgresql.conf" <<'PG_CONF'
listen_addresses = ''
unix_socket_directories = '/socket'
unix_socket_permissions = 0777
port = 55434
PG_CONF
started=1
"$pg/pg_ctl" -D "$data" -l /tmp/postgres.log -w -t 30 start
start_rc=$?
if (( start_rc != 0 )); then exit "$start_rc"; fi
read -r pid <"$data/postmaster.pid"
[[ $pid =~ ^[1-9][0-9]*$ ]] || exit 1
[[ ! -e /socket/ready.pending.json && ! -L /socket/ready.pending.json && ! -e /socket/ready.json && ! -L /socket/ready.json ]] || exit 1
printf '{"owned_pid":%s,"uid":%s,"gid":%s}\n' "$pid" "$(id -u)" "$(id -g)" >/socket/ready.pending.json || exit 1
chmod 0644 /socket/ready.pending.json || exit 1
mv -T -- /socket/ready.pending.json /socket/ready.json || exit 1
while [[ ! -f /socket/stop-request ]]; do sleep 0.1; done
