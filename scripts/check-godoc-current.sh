#!/bin/sh
# Generate or check godoc-current.txt: the committed snapshot of `go doc -all`
# for every public package in this module. CI runs this without --update and

set -eu

gen() {
  for pkg in $(go list ./... | grep -v '/examples/'); do
    echo "## ${pkg}"
    echo
    go doc -all "${pkg}"
    echo
  done
}

case "${1:-}" in
  --update)
    gen > godoc-current.txt
    echo "godoc-current.txt updated"
    ;;
  *)
    expected_file=$(mktemp)
    trap 'rm -f "$expected_file"' EXIT
    gen > "$expected_file"
    if ! diff -u godoc-current.txt "$expected_file"; then
      echo
      echo "godoc-current.txt is out of date." >&2
      echo "Regenerate with: ./scripts/check-godoc-current.sh --update" >&2
      echo "Or: coily exec godoc-update" >&2
      exit 1
    fi
    ;;
esac
