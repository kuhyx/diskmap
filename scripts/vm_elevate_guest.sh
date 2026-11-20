#!/bin/bash
# Runs inside a vmbox guest (as the test user, with passwordless sudo): the
# real pkexec path of diskmap's root delete. A polkit rule lets this user run
# pkexec without an agent. Usage: vm_elevate_guest.sh <diskmap-binary>
set -euo pipefail
readonly bin="$1" port=18765
fail() { echo "FAIL: $*" >&2; exit 1; }

command -v pkexec >/dev/null || sudo pacman -Sy --noconfirm --needed polkit >/dev/null
command -v jq >/dev/null || sudo pacman -Sy --noconfirm --needed jq >/dev/null
sudo install -d -m 755 /etc/polkit-1/rules.d
printf '%s\n' "polkit.addRule(function(a, s) { if (a.id == 'org.freedesktop.policykit.exec' && s.user == '$USER') return polkit.Result.YES; });" \
  | sudo tee /etc/polkit-1/rules.d/49-dm-test.rules >/dev/null
sudo systemctl restart polkit
sudo install -d -m 755 /usr/share/dm-test
echo data | sudo tee /usr/share/dm-test/x >/dev/null

"$bin" --root /usr/share --port "$port" --no-browser >/dev/null &
dm=$!
trap 'kill "$dm" 2>/dev/null || true' EXIT
url="http://127.0.0.1:$port"
for _ in $(seq 100); do curl -sf "$url/" >/dev/null && break; sleep 0.1; done
token="$(curl -sf "$url/" | sed -n 's/.*DISKMAP_TOKEN = "\([0-9a-f]*\)".*/\1/p')"
[[ -n "$token" ]] || fail "no token"
for _ in $(seq 300); do [[ "$(curl -sf -H "X-Token: $token" "$url/api/progress" | jq .progress.done)" == true ]] && break; sleep 0.2; done

del() { curl -sf -H "X-Token: $token" -H "Origin: $url" -H 'Content-Type: application/json' -d "$1" "$url/api/delete"; }

r="$(del '{"paths":["/usr/share/dm-test"]}')"; echo "trash dm-test: $r"
[[ "$(jq '.[0].needs_root' <<<"$r")" == true && -e /usr/share/dm-test/x ]] || fail "no needs_root"
r="$(del '{"paths":["/usr/share/dm-test"],"permanent":true,"elevate":true}')"; echo "elevate dm-test: $r"
[[ "$(jq '.[0].ok' <<<"$r")" == true && ! -e /usr/share/dm-test ]] || fail "elevated delete"
r="$(del '{"paths":["/usr/share/man"]}')"; echo "trash man: $r"
[[ "$(jq '.[0].needs_root and .[0].owned' <<<"$r")" == true && -d /usr/share/man ]] || fail "man not owned/needs_root"
r="$(del '{"paths":["/usr"]}')"; echo "outside root /usr: $r"
echo "VM PASS"
