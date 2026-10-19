#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Interoperability run of ocppctl against the reference implementations of
# otfabric/ocpp-interop (https://github.com/otfabric/ocpp-interop), with the
# samples of their fixtures:
#
#   A. ocppctl as the station  -> each reference CSMS:
#      every station-sent sample with "client call" / "client send", then
#      "client run" answering every CSMS-sent sample the reference CSMS sends.
#   B. each reference station  -> ocppctl as the CSMS ("server start"):
#      every station-sent sample sent by the reference station, then every
#      CSMS-sent sample with "server call --yes".
#
# Usage: scripts/interop.sh [ocpp-interop tag]   (default v0.3.1)
# Needs docker and jq. Writes reports/interop-<tag>.tsv and prints a summary.
# A CALLERROR answer is recorded as "callerror" with its code: a protocol
# success, but not a pass. Nothing is skipped silently.
set -uo pipefail

TAG=${1:-v0.3.1}
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

wait_for() { # seconds command...
	local n=$(($1 * 10)); shift
	for _ in $(seq "$n"); do "$@" >/dev/null 2>&1 && return 0; sleep 0.1; done
	return 1
}

# ---- A: ocppctl station -> reference CSMS ---------------------------------
for sv in mobilityhouse:1.6 mobilityhouse:2.0.1 mobilityhouse:2.1 chargetime:1.6 chargetime:2.0.1 lorenzodonini:1.6 lorenzodonini:2.0.1; do
	stack=${sv%%:*} v=${sv#*:}
	echo "A  ocppctl -> $stack CSMS, OCPP $v"
	docker rm -f oc-csms >/dev/null 2>&1
	docker run -d --name oc-csms -p 127.0.0.1:$CSMS_PORT:9000 "$(image "$stack")" server --version "$v" >/dev/null
	url=ws://127.0.0.1:$CSMS_PORT/ocpp/$ID
	if ! wait_for 30 "$BIN" client probe --url "$url" --insecure-transport --ocpp-version "$v"; then
		emit station-to-csms "$stack" "$v" "(connect)" "fail no connection"
		continue
	fi
	emit station-to-csms "$stack" "$v" "(probe)" pass
	while IFS=$'\t' read -r action kind payload; do
		sub=call; [ "$kind" = send ] && sub=send
		out=$("$BIN" client $sub "$action" --url "$url" --insecure-transport --ocpp-version "$v" --payload "$payload" --format json 2>/dev/null)
		emit station-to-csms "$stack" "$v" "$action" "$(classify_ocppctl $? "$out")"
	done < <(samples "$stack" "$v" station)
	# the simulated station answers what the reference CSMS sends
	"$BIN" client run --url "$url" --insecure-transport --ocpp-version "$v" --no-reconnect --format json >/tmp/oc-run.jsonl 2>&1 &
	RUN=$!
	wait_for 15 grep -q '"event":"booted"' /tmp/oc-run.jsonl
	while IFS=$'\t' read -r action kind payload; do
		doc=$(docker exec oc-csms ocpp-interop call --station "$ID" "$action" --payload "$payload" 2>/dev/null)
		emit csms-to-station "$stack" "$v" "$action" "$(classify_doc "$doc")"
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
		doc=$(docker exec oc-station ocpp-interop call "$action" --payload "$payload" 2>/dev/null)
		r=$(classify_doc "$doc")
		[ -z "$doc" ] && r="fail no result (the adapter may not send $action)"
		emit station-to-csms "$stack" "$v" "$action" "$r"
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
	for (k in keys) printf "%-45s pass %3d  callerror %3d  fail %3d\n", k, n[k SUBSEP "pass"], n[k SUBSEP "callerror"], n[k SUBSEP "fail"] }' "$OUT" | sort
