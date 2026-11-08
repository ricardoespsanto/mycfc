#!/usr/bin/env bash
set -euo pipefail

# herdr-team-ctl.sh
# Team coordination helper for the MyCFC Herdr workspace.

WORKSPACE_LABEL="mycfc-team"
PROJECT_DIR="/home/ricardoes/Projects/mycfcoimbra"

get_workspace_id() {
  local ws_id
  ws_id=$(herdr workspace list | jq -r --arg lbl "$WORKSPACE_LABEL" '.result.workspaces[] | select(.label == $lbl) | .workspace_id' | head -n 1)
  if [[ -z "$ws_id" || "$ws_id" == "null" ]]; then
    echo "Error: Workspace '$WORKSPACE_LABEL' not found. Run ./scripts/setup-herdr-workspace.sh first." >&2
    exit 1
  fi
  echo "$ws_id"
}

get_pane_by_role() {
  local ws_id="$1"
  local role="$2"
  local pane_id
  pane_id=$(herdr pane list --workspace "$ws_id" | jq -r --arg r "$role" '.result.panes[] | select(.label == $r) | .pane_id' | head -n 1)
  if [[ -z "$pane_id" || "$pane_id" == "null" ]]; then
    echo "Error: Pane with role '$role' not found in workspace $ws_id." >&2
    exit 1
  fi
  echo "$pane_id"
}

# Terminal mapping for herdr-app / hc
declare -A HC_TERMINALS=(
  ["lead"]="t1b"
  ["product-owner"]="t1c"
  ["ux-researcher"]="t1d"
  ["product-designer"]="t1f"
  ["backend-domain"]="t1g"
  ["backend-data"]="t1h"
  ["frontend-ui"]="t1j"
  ["frontend-a11y"]="t1k"
  ["qa-engineer"]="t1m"
  ["security-reviewer"]="t1n"
  ["devops-sre"]="t1p"
  ["runtime-console"]="t1q"
)

cmd_status() {
  echo "══════════════════════════════════════════════════════════════════════════════════════════════════"
  echo "  MYCFC HERDR APP TEAM STATUS (mycfcoimbra)"
  echo "══════════════════════════════════════════════════════════════════════════════════════════════════"
  printf "%-18s | %-10s | %-20s | %s\n" "Tab" "Terminal" "Role" "Active Command / State"
  echo "──────────────────────────────────────────────────────────────────────────────────────────────────"
  
  for role in "lead" "product-owner" "ux-researcher" "product-designer" "backend-domain" "backend-data" "frontend-ui" "frontend-a11y" "qa-engineer" "security-reviewer" "devops-sre" "runtime-console"; do
    term="${HC_TERMINALS[$role]}"
    case "$role" in
      lead|product-owner) tab="Lead & PO" ;;
      ux-researcher|product-designer) tab="Design & UX" ;;
      backend-domain|backend-data) tab="Backend" ;;
      frontend-ui|frontend-a11y) tab="Frontend" ;;
      qa-engineer|security-reviewer) tab="QA & Security" ;;
      devops-sre|runtime-console) tab="Ops & Runtime" ;;
    esac
    printf "%-18s | %-10s | %-20s | %s\n" "$tab" "$term" "$role" "$(hc get "$term" --json 2>/dev/null | jq -r '.status // "running"')"
  done
  echo "══════════════════════════════════════════════════════════════════════════════════════════════════"
}

cmd_brief() {
  local target="${1:-all}"
  if [[ "$target" == "all" ]]; then
    for role in "${!HC_TERMINALS[@]}"; do
      term="${HC_TERMINALS[$role]}"
      echo "Briefing $role ($term)..."
      hc send "$term" "$PROJECT_DIR/scripts/herdr-team-brief.sh $role" >/dev/null
    done
  else
    term="${HC_TERMINALS[$target]:-}"
    if [[ -n "$term" ]]; then
      echo "Briefing $target ($term)..."
      hc send "$term" "$PROJECT_DIR/scripts/herdr-team-brief.sh $target" >/dev/null
    fi
  fi
}

cmd_run() {
  local role="$1"
  shift
  local cmd="$*"
  local term="${HC_TERMINALS[$role]:-}"
  if [[ -n "$term" ]]; then
    echo "--> Running in $role ($term): $cmd"
    hc send "$term" "$cmd"
  fi
}

cmd_worktree() {
  local branch="$1"
  local ws_id
  ws_id=$(get_workspace_id)
  echo "--> Creating Git worktree for branch '$branch'..."
  herdr worktree create --workspace "$ws_id" --branch "$branch" --trust-repository --no-focus
}

cmd_dev() {
  cmd_run "runtime-console" "PATH=\"\$PWD/bin:\$PATH\" make dev"
}

cmd_verify() {
  cmd_run "qa-engineer" "make verify"
}

usage() {
  echo "Usage: $0 <command> [arguments]"
  echo ""
  echo "Commands:"
  echo "  status                 Display the full team status matrix across all tabs & panes"
  echo "  brief [role|all]       Display role briefing banner in the specified pane (or all)"
  echo "  run <role> <command>   Execute a command in the specified role's pane"
  echo "  worktree <branch>      Create an isolated git worktree for a feature branch"
  echo "  dev                    Launch live reload dev server in runtime-console"
  echo "  verify                 Run full make verify in qa-engineer pane"
  echo ""
  echo "Available roles:"
  echo "  lead, product-owner, ux-researcher, product-designer,"
  echo "  backend-domain, backend-data, frontend-ui, frontend-a11y,"
  echo "  qa-engineer, security-reviewer, devops-sre, runtime-console"
  exit 1
}

case "${1:-}" in
  status)   cmd_status ;;
  brief)    cmd_brief "${2:-all}" ;;
  run)      [[ $# -ge 3 ]] || usage; cmd_run "$2" "${@:3}" ;;
  worktree) [[ $# -ge 2 ]] || usage; cmd_worktree "$2" ;;
  dev)      cmd_dev ;;
  verify)   cmd_verify ;;
  *)        usage ;;
esac
