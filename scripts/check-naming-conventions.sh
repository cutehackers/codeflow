#!/usr/bin/env bash

set -euo pipefail

# Planning labels are metadata. They are not implementation vocabulary.
label_pattern='(^|[^[:alnum:]])(VS|CF)[_-]?[0-9]+([^[:alnum:]]|$)|(^|[^[:alnum:]])(RFLSC|LPCA)([^[:alnum:]]|$)'

is_code_path() {
  case "$1" in
    *.go|*.dart|*.ts|*.tsx|*.js|*.jsx) return 0 ;;
    *) return 1 ;;
  esac
}

check_path() {
  local path="$1"
  if ! is_code_path "$path"; then
    return 0
  fi
  if printf '%s\n' "$path" | grep -Eiq "$label_pattern"; then
    printf 'forbidden design-plan label in code path: %s\n' "$path" >&2
    return 1
  fi
}

check_declarations() {
  local path="$1"
  local declaration_lines

  if git ls-files --error-unmatch -- "$path" >/dev/null 2>&1; then
    declaration_lines=''
  else
    declaration_lines=$(sed 's/^/+/' "$path")
  fi

  case "$path" in
    *.go)
      if git ls-files --error-unmatch -- "$path" >/dev/null 2>&1; then
        declaration_lines=$(git diff --unified=0 HEAD -- "$path" | grep -E '^\+[^+].*(package|func|type|var|const)[[:space:]]+[A-Za-z_][A-Za-z0-9_]*' || true)
      else
        declaration_lines=$(printf '%s\n' "$declaration_lines" | grep -E '^\+[^+].*(package|func|type|var|const)[[:space:]]+[A-Za-z_][A-Za-z0-9_]*' || true)
      fi
      ;;
    *.dart|*.ts|*.tsx|*.js|*.jsx)
      if git ls-files --error-unmatch -- "$path" >/dev/null 2>&1; then
        declaration_lines=$(git diff --unified=0 HEAD -- "$path" | grep -E '^\+[^+].*(class|function|const|let|var)[[:space:]]+[A-Za-z_$][A-Za-z0-9_$]*' || true)
      else
        declaration_lines=$(printf '%s\n' "$declaration_lines" | grep -E '^\+[^+].*(class|function|const|let|var)[[:space:]]+[A-Za-z_$][A-Za-z0-9_$]*' || true)
      fi
      ;;
    *)
      return 0
      ;;
  esac

  if [ -n "$declaration_lines" ] && printf '%s\n' "$declaration_lines" | grep -Eiq "$label_pattern"; then
    printf 'forbidden design-plan label in changed declaration: %s\n' "$path" >&2
    printf '%s\n' "$declaration_lines" >&2
    return 1
  fi
}

status=0
while IFS= read -r path; do
  check_path "$path" || status=1
  check_declarations "$path" || status=1
done < <(
  {
    git diff --name-only --diff-filter=ACMRTUXB HEAD
    git ls-files --others --exclude-standard
  } | sort -u
)

exit "$status"
