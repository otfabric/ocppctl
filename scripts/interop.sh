#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Interoperability run of ocppctl against the reference implementations of
# otfabric/ocpp-interop (https://github.com/otfabric/ocpp-interop), with the
# samples of their fixtures:
#
#   A. ocppctl as the station  -> each reference CSMS (CitrineOS answers by
#      itself, not from the fixture: its answers are what CitrineOS does):
#      every station-sent sample with "client call" / "client send", then
#      "client run" answering every CSMS-sent sample the reference CSMS sends.
#   B. each reference station  -> ocppctl as the CSMS ("server start"):
#      every station-sent sample sent by the reference station, then every
#      CSMS-sent sample with "server call --yes".
#
# Usage: scripts/interop.sh [ocpp-interop tag]   (default v0.4.1)
# Needs docker and jq. Writes reports/interop-<tag>.tsv and prints a summary.
# A CALLERROR answer is recorded as "callerror" with its code: a protocol
# success, but not a pass. A message the reference could not or would not send
# (ocppctl never received it) is "skip", with the reference's reason. Nothing
# is skipped silently.
set -uo pipefail

TAG=${1:-v0.4.1}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=$(mktemp -d)/ocppctl
OUT=$ROOT/reports/interop-$TAG.tsv
ID=OT-FABRIC-TEST-001
CSMS_PORT=9300
SERVER_PORT=9400
SOCK=$(mktemp -d)/s.sock
mkdir -p "$ROOT/reports"
(cd "$ROOT" && go build -o "$BIN" .) || exit 1
printf 'direction\tstack\tversion\taction\tresult\tdetail\n' >"$OUT"

record() { printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$@" >>"$OUT"; }
image() { echo "ghcr.io/otfabric/ocpp-interop-$1:$TAG"; }
cleanup() { docker rm -f oc-csms oc-station >/dev/null 2>&1; [ -n "${SRV:-}" ] && kill "$SRV" 2>/dev/null; [ -n "${RUN:-}" ] && kill "$RUN" 2>/dev/null; }
trap cleanup EXIT

samples() { # stack version from -> lines "action<TAB>kind<TAB>payload"; kind is call or send
	local sends
	sends=$("$BIN" actions --ocpp-version "$2" --format json | jq -c '[.actions[] | select(.kind == "send") | .name]')
	docker run --rm "$(image "$1")" print-fixture --version "$2" |
		jq -r --arg from "$3" --argjson sends "$sends" '.samples | to_entries[] | select(.value.from == $from) |
			[.value.action, (if (.value.action | IN($sends[])) then "send" else "call" end), (.value.payload | tojson)] | @tsv' |
		sort -u -t$'\t' -k1,1
}

# result of an ocppctl command: exit code and its JSON on stdout
classify_ocppctl() { # code stdout
	local code=$1 out=$2
	case $code in
	0) echo "pass" ;;
	6) local ce
		ce=$(jq -r 'select(.error.code == "call_error") | .ocpp.error_code' <<<"$out" 2>/dev/null)
		if [ -n "$ce" ]; then echo "callerror $ce"; else echo "fail $(jq -r '.error.code + ": " + .error.message' <<<"$out" 2>/dev/null | head -c 200)"; fi ;;
	*) echo "fail exit $code $(jq -r '.error.code + ": " + .error.message' <<<"$out" 2>/dev/null | head -c 200)" ;;
	esac
}

# result of an ocpp-interop result document
classify_doc() { # json
	local doc=$1
	if [ "$(jq -r .ok <<<"$doc" 2>/dev/null)" = true ]; then echo pass; return; fi
	local ce
	ce=$(jq -r '.callError.code // empty' <<<"$doc" 2>/dev/null)
	if [ -n "$ce" ]; then echo "callerror $ce"; else echo "fail $(jq -c '.error' <<<"$doc" 2>/dev/null | head -c 200)"; fi
}

emit() { # direction stack version action result
	local r=$5
	record "$1" "$2" "$3" "$4" "${r%% *}" "$( [ "$r" = "${r#* }" ] && echo "" || echo "${r#* }")"
}

# settle waits until the reference CSMS has seen every earlier connection close
# (as many connection-closed events as connection-opened): CitrineOS mixes up a
# new connection with one of the same identity whose close it has not handled.
settle() {
	local opened closed
	for _ in $(seq 100); do
		opened=$(docker logs oc-csms 2>/dev/null | grep -c '"event":"connection-opened"')
		closed=$(docker logs oc-csms 2>/dev/null | grep -c '"event":"connection-closed"')
		[ "$closed" -ge "$opened" ] && return 0
		sleep 0.1
	done
}

# received LOG ACTION counts the CALLs and SENDs of ACTION ocppctl received, in
# the event log of `client run` or `server start`.
received() {
	grep -E '"event":"(call|send)_received"' "$1" 2>/dev/null | grep -c "\"action\":\"$2\"" || true
}

# by_reference RESULT BEFORE AFTER: a result that is not a pass, for a CALL
# ocppctl never received, is the reference not sending it (no API for it, its
# library cannot encode it, it refused the payload): a skip with the
# reference's reason, not a failure of ocppctl.
by_reference() {
	local r=$1 before=$2 after=$3
	if [ "${r%% *}" != pass ] && [ "${r%% *}" != callerror ] && [ "$after" -le "$before" ]; then
		echo "skip the reference did not send it: ${r#* }"
	else
		echo "$r"
	fi
}

booted() { grep -c '"event":"booted"' /tmp/oc-run.jsonl 2>/dev/null || echo 0; }

wait_for() { # seconds command...
	local n=$(($1 * 10)); shift
	for _ in $(seq "$n"); do "$@" >/dev/null 2>&1 && return 0; sleep 0.1; done
	return 1
}

# ---- A: ocppctl station -> reference CSMS ---------------------------------
for sv in mobilityhouse:1.6 mobilityhouse:2.0.1 mobilityhouse:2.1 chargetime:1.6 chargetime:2.0.1 lorenzodonini:1.6 lorenzodonini:2.0.1 \
	citrineos:1.6 citrineos:2.0.1 citrineos:2.1; do
	stack=${sv%%:*} v=${sv#*:}
	echo "A  ocppctl -> $stack CSMS, OCPP $v"
	docker rm -f oc-csms >/dev/null 2>&1
	docker run -d --name oc-csms -p 127.0.0.1:$CSMS_PORT:9000 "$(image "$stack")" server --version "$v" >/dev/null
	url=ws://127.0.0.1:$CSMS_PORT/ocpp/$ID
	ready=30
	[ "$stack" = citrineos ] && ready=240 # PostgreSQL, RabbitMQ and CitrineOS start in the container
	if ! wait_for $ready "$BIN" client probe --url "$url" --insecure-transport --ocpp-version "$v"; then
		emit station-to-csms "$stack" "$v" "(connect)" "fail no connection"
		continue
	fi
	emit station-to-csms "$stack" "$v" "(probe)" pass
	while IFS=$'\t' read -r action kind payload; do
		sub=call; [ "$kind" = send ] && sub=send
		settle
		out=$("$BIN" client $sub "$action" --url "$url" --insecure-transport --ocpp-version "$v" --payload "$payload" --format json 2>/dev/null)
		emit station-to-csms "$stack" "$v" "$action" "$(classify_ocppctl $? "$out")"
	done < <(samples "$stack" "$v" station)
	# the simulated station answers what the reference CSMS sends
	settle
	"$BIN" client run --url "$url" --insecure-transport --ocpp-version "$v" --no-reconnect --format json >/tmp/oc-run.jsonl 2>&1 &
	RUN=$!
	wait_for 15 grep -q '"event":"booted"' /tmp/oc-run.jsonl
	while IFS=$'\t' read -r action kind payload; do
		before=$(booted)
		got=$(received /tmp/oc-run.jsonl "$action")
		doc=$(docker exec oc-csms ocpp-interop call --station "$ID" "$action" --payload "$payload" 2>/dev/null)
		emit csms-to-station "$stack" "$v" "$action" "$(by_reference "$(classify_doc "$doc")" "$got" "$(received /tmp/oc-run.jsonl "$action")")"
		# a Reset restarts the station: wait until it has booted again
		[ "$action" = Reset ] && wait_for 60 sh -c "[ \$(grep -c '\"event\":\"booted\"' /tmp/oc-run.jsonl) -gt $before ]"
	done < <(samples "$stack" "$v" csms)
	kill "$RUN" 2>/dev/null; wait "$RUN" 2>/dev/null; RUN=
	docker rm -f oc-csms >/dev/null 2>&1
done

# ---- B: reference station -> ocppctl CSMS ---------------------------------
"$BIN" server start --listen 0.0.0.0:$SERVER_PORT --path /ocpp --no-auth --insecure-transport --control-socket "$SOCK" --format json >/tmp/oc-server.jsonl 2>&1 &
SRV=$!
wait_for 10 test -S "$SOCK"
for sv in mobilityhouse:1.6 mobilityhouse:2.0.1 mobilityhouse:2.1 chargetime:1.6 chargetime:2.0.1 lorenzodonini:1.6 lorenzodonini:2.0.1 everest:1.6 everest:2.0.1 everest:2.1; do
	stack=${sv%%:*} v=${sv#*:}
	echo "B  $stack station -> ocppctl, OCPP $v"
	docker rm -f oc-station >/dev/null 2>&1
	docker run -d --name oc-station --add-host host.docker.internal:host-gateway "$(image "$stack")" \
		client run --version "$v" --url ws://host.docker.internal:$SERVER_PORT/ocpp >/dev/null
	if ! wait_for 60 sh -c "'$BIN' server stations --control-socket '$SOCK' --format json | jq -e '.stations[] | select(.identity == \"$ID\" and .version == \"$v\")'"; then
		emit station-to-csms "$stack" "$v" "(connect)" "fail no connection"
		continue
	fi
	while IFS=$'\t' read -r action kind payload; do
		got=$(received /tmp/oc-server.jsonl "$action")
		doc=$(docker exec oc-station ocpp-interop call "$action" --payload "$payload" 2>/dev/null)
		r=$(classify_doc "$doc")
		[ -z "$doc" ] && r="fail no result (the adapter may not send $action)"
		emit station-to-csms "$stack" "$v" "$action" "$(by_reference "$r" "$got" "$(received /tmp/oc-server.jsonl "$action")")"
	done < <(samples "$stack" "$v" station)
	while IFS=$'\t' read -r action kind payload; do
		out=$("$BIN" server call "$action" --station "$ID" --control-socket "$SOCK" --payload "$payload" --yes --format json 2>/dev/null)
		emit csms-to-station "$stack" "$v" "$action" "$(classify_ocppctl $? "$out")"
	done < <(samples "$stack" "$v" csms)
	docker rm -f oc-station >/dev/null 2>&1
	sleep 0.5
done

echo
echo "results in $OUT"
awk -F'\t' 'NR > 1 { k = $2 " " $3 " " $1; keys[k] = 1; n[k SUBSEP $5]++ } END {
	for (k in keys) printf "%-45s pass %3d  callerror %3d  skip %3d  fail %3d\n", k, n[k SUBSEP "pass"], n[k SUBSEP "callerror"], n[k SUBSEP "skip"], n[k SUBSEP "fail"] }' "$OUT" | sort
