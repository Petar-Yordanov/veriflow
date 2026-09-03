#!/bin/zsh

# Read only explicitly selected project files in full and copy the combined
# contents to the clipboard. Output paths are relative to PROJECT_ROOT.

set -euo pipefail

export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

COPY_TO_CLIPBOARD=1
PRINT_TO_STDOUT=0
OUTPUT_FILE=""
ALLOW_SENSITIVE=0

usage() {
  cat <<'USAGE'
Usage:
  ./read-into-clipboard.zsh [options] PROJECT_ROOT FILE [FILE ...]

Examples:
  ./read-into-clipboard.zsh ~/Projects/my-app \
    src/auth/service.ts \
    src/auth/service.test.ts

  ./read-into-clipboard.zsh --stdout /work/project \
    pkg/server/server.go \
    pkg/server/server_test.go

PROJECT_ROOT may be relative or absolute. Each FILE may be relative to
PROJECT_ROOT or an absolute path inside PROJECT_ROOT. Only the listed files are
read. Directories, missing paths, symlinks, paths outside PROJECT_ROOT, and
binary files are rejected.

Options:
  --stdout             Print the bundle instead of copying it.
  --output FILE        Also save the bundle to FILE.
  --allow-sensitive    Allow explicitly selected files such as .env or keys.
  -h, --help           Show this help.
USAGE
}

canonical_dir() {
  (cd -P "$1" 2>/dev/null && pwd -P)
}

canonical_file() {
  local input_path="$1"
  local dir base

  dir="${input_path:h}"
  base="${input_path:t}"

  (cd -P "$dir" 2>/dev/null && printf '%s/%s\n' "$(pwd -P)" "$base")
}

copy_file_to_clipboard() {
  local source="$1"

  if command -v pbcopy >/dev/null 2>&1; then
    pbcopy < "$source"
  elif command -v wl-copy >/dev/null 2>&1; then
    wl-copy < "$source"
  elif command -v xclip >/dev/null 2>&1; then
    xclip -selection clipboard < "$source"
  elif command -v xsel >/dev/null 2>&1; then
    xsel --clipboard --input < "$source"
  else
    printf '%s\n' "error: no supported clipboard command found" >&2
    printf '%s\n' "Install/use pbcopy, wl-copy, xclip, or xsel; or run with --stdout." >&2
    return 1
  fi
}

is_sensitive_file() {
  local rel="$1"
  local name="${rel##*/}"

  [ "$ALLOW_SENSITIVE" -eq 1 ] && return 1

  case "$name" in
    .env|.env.*)
      case "$name" in
        .env.example|.env.sample|.env.template|.env.defaults) return 1 ;;
        *) return 0 ;;
      esac
      ;;
    .npmrc|.pypirc|.netrc|credentials|credentials.*|secrets|secrets.*|\
    id_rsa|id_dsa|id_ecdsa|id_ed25519|*.pem|*.key|*.p12|*.pfx|*.jks|*.keystore)
      return 0
      ;;
  esac

  return 1
}

is_text_file() {
  local input_file="$1"
  local mime=""

  [ ! -s "$input_file" ] && return 0

  if command -v file >/dev/null 2>&1; then
    mime="$(file -b --mime-type "$input_file" 2>/dev/null || true)"

    case "$mime" in
      text/*|\
      application/json|\
      application/xml|\
      application/javascript|\
      application/x-javascript|\
      application/sql|\
      application/toml|\
      application/yaml|\
      inode/x-empty)
        return 0
        ;;
    esac
  fi

  if command -v grep >/dev/null 2>&1; then
    LC_ALL=C grep -Iq . "$input_file" 2>/dev/null && return 0
  fi

  return 1
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --stdout)
      PRINT_TO_STDOUT=1
      COPY_TO_CLIPBOARD=0
      shift
      ;;
    --output)
      [ "$#" -ge 2 ] || { printf '%s\n' "error: --output requires a path" >&2; exit 2; }
      OUTPUT_FILE="$2"
      shift 2
      ;;
    --allow-sensitive)
      ALLOW_SENSITIVE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      break
      ;;
    -*)
      printf 'error: unknown option: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
    *)
      break
      ;;
  esac
done

[ "$#" -ge 2 ] || {
  printf '%s\n' "error: PROJECT_ROOT and at least one FILE are required" >&2
  usage >&2
  exit 2
}

PROJECT_ROOT_INPUT="$1"
shift

[ -d "$PROJECT_ROOT_INPUT" ] || {
  printf 'error: project root is not a directory: %s\n' "$PROJECT_ROOT_INPUT" >&2
  exit 1
}

ROOT="$(canonical_dir "$PROJECT_ROOT_INPUT")"
OUTPUT_ABS=""
if [ -n "$OUTPUT_FILE" ]; then
  output_parent="$(dirname "$OUTPUT_FILE")"
  mkdir -p "$output_parent"
  OUTPUT_ABS="$(canonical_dir "$output_parent")/$(basename "$OUTPUT_FILE")"
fi

TMP_BUNDLE="$(mktemp "${TMPDIR:-/tmp}/selected-project-files.XXXXXX")"
TMP_SELECTION="$(mktemp "${TMPDIR:-/tmp}/selected-project-list.XXXXXX")"
TMP_SEEN="$(mktemp "${TMPDIR:-/tmp}/selected-project-seen.XXXXXX")"
trap 'rm -f "$TMP_BUNDLE" "$TMP_SELECTION" "$TMP_SEEN"' EXIT INT TERM HUP

for requested in "$@"; do
  case "$requested" in
    *$'\n'*|*$'\t'*)
      printf 'error: tabs and newlines are not supported in selected paths: %s\n' "$requested" >&2
      exit 1
      ;;
  esac

  case "$requested" in
    /*) candidate="$requested" ;;
    *)  candidate="$ROOT/$requested" ;;
  esac

  [ -e "$candidate" ] || {
    printf 'error: selected file does not exist: %s\n' "$requested" >&2
    exit 1
  }

  [ ! -L "$candidate" ] || {
    printf 'error: symlinks are not read: %s\n' "$requested" >&2
    exit 1
  }

  [ -f "$candidate" ] || {
    printf 'error: selected path is not a regular file: %s\n' "$requested" >&2
    exit 1
  }

  abs_path="$(canonical_file "$candidate")"

  if [ "$ROOT" = "/" ]; then
    rel_path="${abs_path#/}"
  else
    case "$abs_path" in
      "$ROOT"/*) rel_path="${abs_path#"$ROOT"/}" ;;
      *)
        printf 'error: selected path is outside the project root: %s\n' "$requested" >&2
        exit 1
        ;;
    esac
  fi

  is_sensitive_file "$rel_path" && {
    printf 'error: refusing potentially sensitive file: %s\n' "$rel_path" >&2
    printf '%s\n' "Use --allow-sensitive only when you intentionally want it copied." >&2
    exit 1
  }

  is_text_file "$abs_path" || {
    printf 'error: selected file appears to be binary or non-text: %s\n' "$rel_path" >&2
    exit 1
  }

  # Preserve the caller's order but skip duplicates.
  if ! grep -Fqx "$abs_path" "$TMP_SEEN"; then
    printf '%s\n' "$abs_path" >> "$TMP_SEEN"
    printf '%s\t%s\n' "$abs_path" "$rel_path" >> "$TMP_SELECTION"
  fi
done

count="$(wc -l < "$TMP_SELECTION" | tr -d '[:space:]')"
[ "$count" -gt 0 ] || {
  printf '%s\n' "error: no unique files were selected" >&2
  exit 1
}

while IFS=$'\t' read -r abs_path rel_path; do
  printf '===== FILE: %s =====\n' "$rel_path" >> "$TMP_BUNDLE"
  cat "$abs_path" >> "$TMP_BUNDLE"
  printf '\n===== End of file: %s =====\n\n' "$rel_path" >> "$TMP_BUNDLE"
done < "$TMP_SELECTION"

if [ -n "$OUTPUT_FILE" ]; then
  cp "$TMP_BUNDLE" "$OUTPUT_ABS"
fi

if [ "$PRINT_TO_STDOUT" -eq 1 ]; then
  cat "$TMP_BUNDLE"
elif [ "$COPY_TO_CLIPBOARD" -eq 1 ]; then
  copy_file_to_clipboard "$TMP_BUNDLE"
fi

bundle_bytes="$(wc -c < "$TMP_BUNDLE" | tr -d '[:space:]')"
printf 'Read %s selected files (%s bytes) from: %s\n' "$count" "$bundle_bytes" "$ROOT" >&2
if [ "$COPY_TO_CLIPBOARD" -eq 1 ]; then
  printf '%s\n' "Copied selected file contents to clipboard." >&2
fi
if [ -n "$OUTPUT_FILE" ]; then
  printf 'Saved bundle to: %s\n' "$OUTPUT_ABS" >&2
fi

