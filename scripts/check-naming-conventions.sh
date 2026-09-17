#!/usr/bin/env bash

set -euo pipefail

# Planning labels are metadata. They are not implementation vocabulary.
label_pattern='(^|[^[:alnum:]])(VS|CF)[_-]?[0-9]+([^[:alnum:]]|$)|(^|[^[:alnum:]])(RFLSC|LPCA)([^[:alnum:]]|$)'

# Anti-pattern: past-verb prefix or ad-hoc past participle naming instead of domain + role/context
# (e.g. saved_views.go, SavedView, handleSavedViews).
past_verb_path_pattern='(^|[/_])saved_views?(\.|$)'
past_verb_decl_pattern='(^|[^[:alnum:]])(handleSavedView[s]?|[sS]avedViews?)([^[:alnum:]]|$)'

is_code_path() {
  case "$1" in
    *.go|*.dart|*.ts|*.tsx|*.js|*.jsx|*.svelte) return 0 ;;
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
  if printf '%s\n' "$path" | grep -Eiq "$past_verb_path_pattern"; then
    printf 'forbidden past-verb naming pattern in code path: %s (use domain + role/context, e.g. task_view_persistence.go)\n' "$path" >&2
    return 1
  fi
}

check_handle_method_declarations() {
  local path="$1"
  local lines="$2"
  local ext="${path##*.}"

  [ -z "$lines" ] && return 0
  if ! printf '%s\n' "$lines" | grep -Eq '[hH]andle'; then
    return 0
  fi
  local candidate_lines
  candidate_lines=$(printf '%s\n' "$lines" | grep -E '[hH]andle' || true)

  while IFS= read -r line; do
    [ -z "$line" ] && continue

    # Strip leading + and leading whitespace
    local trimmed
    trimmed=$(printf '%s\n' "$line" | sed -E 's/^\+[[:space:]]*//')

    # Skip comments
    if printf '%s\n' "$trimmed" | grep -Eq '^(//|/\*|\*|#|///)'; then
      continue
    fi

    # Allow Handler() noun accessor or standard http.Handler / HandlerFunc
    if printf '%s\n' "$trimmed" | grep -Eiq '(func([[:space:]]*\([^)]+\))?[[:space:]]+[hH]andler([[:space:]]*\(|Func)|type[[:space:]]+[hH]andler\b|Handler[[:space:]]*\(\)[[:space:]]+http\.Handler|\.Handler\b)'; then
      continue
    fi

    local matched=false
    case "$ext" in
      go)
        # func HandleFoo / func (r Receiver) HandleFoo
        if printf '%s\n' "$trimmed" | grep -Eq '(^|[^.[:alnum:]_])func([[:space:]]*\([^)]+\))?[[:space:]]+[hH]andle[A-Za-z0-9_]*'; then
          matched=true
        # Interface method signature: HandleFoo(...) with return type / error
        elif printf '%s\n' "$trimmed" | grep -Eq '^[[:space:]]*[hH]andle[A-Za-z0-9_]*\([^)]*\)[[:space:]]+([A-Za-z0-9_*.]+|\([^)]*\))'; then
          matched=true
        fi
        ;;
      dart)
        # Function or method declaration: [Type] handleFoo(...)
        if printf '%s\n' "$trimmed" | grep -Eq '(^|[^.[:alnum:]_$])([A-Za-z0-9_<>]+[[:space:]]+)+[hH]andle[A-Za-z0-9_]*[[:space:]]*\('; then
          matched=true
        fi
        ;;
      ts|tsx|js|jsx|svelte)
        # function handleFoo, async function handleFoo, export function handleFoo
        if printf '%s\n' "$trimmed" | grep -Eq '(^|[^.[:alnum:]_$])(export[[:space:]]+)?(async[[:space:]]+)?function[[:space:]]+[hH]andle[A-Za-z0-9_]*'; then
          matched=true
        # const/let/var handleFoo = / :
        elif printf '%s\n' "$trimmed" | grep -Eq '(^|[^.[:alnum:]_$])(export[[:space:]]+)?(const|let|var)[[:space:]]+[hH]andle[A-Za-z0-9_]*([[:space:]]*=|[[:space:]]*:)'; then
          matched=true
        # Destructured prop: let { handleFoo } = ...
        elif printf '%s\n' "$trimmed" | grep -Eq '(^|[^.[:alnum:]_$])(const|let|var)[[:space:]]+\{[^}]*\b[hH]andle[A-Za-z0-9_]*\b'; then
          matched=true
        # Class or object method: (export)? (public|private|protected|static|override|async)* handleFoo(...)
        elif printf '%s\n' "$trimmed" | grep -Eq '(^|[^.[:alnum:]_$])(export[[:space:]]+)?(public[[:space:]]+|private[[:space:]]+|protected[[:space:]]+|static[[:space:]]+|override[[:space:]]+|async[[:space:]]+)*[hH]andle[A-Za-z0-9_]*[[:space:]]*\('; then
          matched=true
        # Object method / property: handleFoo: (...) => or handleFoo: function
        elif printf '%s\n' "$trimmed" | grep -Eq '(^|[^.[:alnum:]_$])[hH]andle[A-Za-z0-9_]*[[:space:]]*:[[:space:]]*'; then
          matched=true
        fi
        ;;
    esac

    if [ "$matched" = true ]; then
      printf 'forbidden generic handle*** method naming in changed declaration: %s (use on*** for callbacks, serve*** for HTTP endpoints, dispatch*** for routing, etc.)\n' "$path" >&2
      printf '%s\n' "$line" >&2
      return 1
    fi
  done <<< "$candidate_lines"

  return 0
}

check_declarations() {
  local path="$1"
  local old_path="${2:-}"
  local added_lines
  local declaration_lines

  if [ -n "$old_path" ]; then
    added_lines=$(git diff -M --unified=0 HEAD -- "$old_path" "$path" | grep -E '^\+[^+]' || true)
  elif git ls-files --error-unmatch -- "$path" >/dev/null 2>&1; then
    added_lines=$(git diff -M --unified=0 HEAD -- "$path" | grep -E '^\+[^+]' || true)
  else
    added_lines=$(sed 's/^/+/' "$path")
  fi

  case "$path" in
    *.go)
      declaration_lines=$(printf '%s\n' "$added_lines" | grep -E '^\+[^+]*\b(package|type|var|const|func)[[:space:]]+(\([^)]+\)[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*' || true)
      ;;
    *.dart|*.ts|*.tsx|*.js|*.jsx|*.svelte)
      declaration_lines=$(printf '%s\n' "$added_lines" | grep -E '^\+[^+]*\b(class|function|const|let|var)[[:space:]]+[A-Za-z_$][A-Za-z0-9_$]*' || true)
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
  if [ -n "$declaration_lines" ] && printf '%s\n' "$declaration_lines" | grep -Eiq "$past_verb_decl_pattern"; then
    printf 'forbidden past-verb naming in changed declaration: %s (use domain + role/context)\n' "$path" >&2
    printf '%s\n' "$declaration_lines" >&2
    return 1
  fi
  check_handle_method_declarations "$path" "$added_lines" || return 1
}

status=0
while IFS=$'\t' read -r status_code p1 p2; do
  path=""
  old_path=""
  if [[ "$status_code" =~ ^R ]]; then
    old_path="$p1"
    path="$p2"
  elif [ "$status_code" = "?" ]; then
    path="$p1"
  elif [ -n "$p1" ]; then
    path="$p1"
  fi
  [ -z "$path" ] && continue

  check_path "$path" || status=1
  check_declarations "$path" "$old_path" || status=1
done < <(
  {
    git diff -M --name-status --diff-filter=ACMRTUXB HEAD
    git ls-files --others --exclude-standard | sed 's/^/?\t/'
  }
)

exit "$status"
