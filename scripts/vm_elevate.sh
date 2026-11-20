#!/bin/bash
# Sandbox-first check of the pkexec delete: builds diskmap, copies it and the
# guest script into a fresh vmbox BIOS guest, runs it, then powers it off.
set -euo pipefail
cd "$(dirname "$0")/.."
readonly name="${VM_NAME:-dmelev}"
# The guest is disposable, so the guest script's partial pacman -Sy is fine.
tmp="$(mktemp -d)"
cleanup() {
  vm run "$name" 'sudo systemctl poweroff' >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT
go build -trimpath -o "$tmp/diskmap" .
vm list | awk '{print $1}' | grep -qx "$name" || vm new "$name"
vm reset "$name"
vm start "$name"
vm scp "$name" "$tmp/diskmap" /tmp/diskmap
vm scp "$name" scripts/vm_elevate_guest.sh /tmp/vm_elevate_guest.sh
# vm run reports a verdict, not the script's exit code: match the PASS line.
out="$(vm run "$name" 'bash /tmp/vm_elevate_guest.sh /tmp/diskmap' 2>&1)"
echo "$out"
grep -q '^VM PASS' <<<"$out"
