#!/usr/bin/env bash
set -euo pipefail

# setup-herdr-workspace.sh
# Sets up or refreshes the MyCFC Software Development Team workspace in Herdr.

WORKSPACE_LABEL="mycfc-team"
PROJECT_DIR="/home/ricardoes/Projects/mycfcoimbra"

command -v herdr >/dev/null 2>&1 || {
  echo "Error: herdr CLI not found in PATH." >&2
  exit 1
}

# Ensure Herdr server is running
if ! herdr status >/dev/null 2>&1; then
  echo "Error: Herdr server is not running or unreachable." >&2
  exit 1
fi

echo "==> Configuring Herdr workspace: $WORKSPACE_LABEL"

# Find existing workspace or create a new one
WS_LIST=$(herdr workspace list)
WS_ID=$(echo "$WS_LIST" | jq -r --arg lbl "$WORKSPACE_LABEL" '.result.workspaces[] | select(.label == $lbl) | .workspace_id' | head -n 1)

if [[ -z "$WS_ID" || "$WS_ID" == "null" ]]; then
  echo "--> Creating new workspace '$WORKSPACE_LABEL'..."
  CREATE_RES=$(herdr workspace create --cwd "$PROJECT_DIR" --label "$WORKSPACE_LABEL" --no-focus)
  WS_ID=$(echo "$CREATE_RES" | jq -r '.result.workspace.workspace_id')
  echo "--> Created workspace ID: $WS_ID"
else
  echo "--> Found existing workspace ID: $WS_ID"
fi

# Define the 6 tabs and their two panes
declare -A TAB_LABELS=(
  [1]="Delivery Lead & PO"
  [2]="Design & UX"
  [3]="Backend"
  [4]="Frontend"
  [5]="QA & Security"
  [6]="Ops & Runtime"
)

declare -A PANE_ROLES_LEFT=(
  [1]="lead"
  [2]="ux-researcher"
  [3]="backend-domain"
  [4]="frontend-ui"
  [5]="qa-engineer"
  [6]="devops-sre"
)

declare -A PANE_ROLES_RIGHT=(
  [1]="product-owner"
  [2]="product-designer"
  [3]="backend-data"
  [4]="frontend-a11y"
  [5]="security-reviewer"
  [6]="runtime-console"
)

# Inspect existing tabs in the workspace
CURRENT_TABS_JSON=$(herdr tab list --workspace "$WS_ID")
EXISTING_TAB_COUNT=$(echo "$CURRENT_TABS_JSON" | jq -r '.result.tabs | length')

for i in {1..6}; do
  TAB_LABEL="${TAB_LABELS[$i]}"
  LEFT_ROLE="${PANE_ROLES_LEFT[$i]}"
  RIGHT_ROLE="${PANE_ROLES_RIGHT[$i]}"

  # Check if tab exists
  TAB_ID=$(echo "$CURRENT_TABS_JSON" | jq -r --argjson idx "$i" '.result.tabs[$idx - 1].tab_id // empty')

  if [[ -z "$TAB_ID" || "$TAB_ID" == "null" ]]; then
    echo "--> Creating Tab $i: $TAB_LABEL..."
    NEW_TAB_JSON=$(herdr tab create --workspace "$WS_ID" --cwd "$PROJECT_DIR" --label "$TAB_LABEL" --no-focus)
    TAB_ID=$(echo "$NEW_TAB_JSON" | jq -r '.result.tab.tab_id')
    ROOT_PANE=$(echo "$NEW_TAB_JSON" | jq -r '.result.root_pane.pane_id')
  else
    herdr tab rename "$TAB_ID" "$TAB_LABEL" >/dev/null
  fi

  # Query panes in this tab
  PANES_JSON=$(herdr pane list --workspace "$WS_ID")
  TAB_PANES=($(echo "$PANES_JSON" | jq -r --arg tid "$TAB_ID" '.result.panes[] | select(.tab_id == $tid) | .pane_id'))

  PANE_COUNT=${#TAB_PANES[@]}
  if [[ $PANE_COUNT -eq 0 ]]; then
    echo "Warning: no panes found for tab $TAB_ID"
    continue
  fi

  P1="${TAB_PANES[0]}"
  herdr pane rename "$P1" "$LEFT_ROLE" >/dev/null

  if [[ $PANE_COUNT -eq 1 ]]; then
    echo "--> Splitting pane for $RIGHT_ROLE in tab $TAB_LABEL..."
    SPLIT_JSON=$(herdr pane split "$P1" --direction right --cwd "$PROJECT_DIR" --no-focus)
    P2=$(echo "$SPLIT_JSON" | jq -r '.result.pane.pane_id')
    herdr pane rename "$P2" "$RIGHT_ROLE" >/dev/null
  else
    P2="${TAB_PANES[1]}"
    herdr pane rename "$P2" "$RIGHT_ROLE" >/dev/null
  fi

  # Run briefing cards
  herdr pane run "$P1" "$PROJECT_DIR/scripts/herdr-team-brief.sh $LEFT_ROLE" >/dev/null
  herdr pane run "$P2" "$PROJECT_DIR/scripts/herdr-team-brief.sh $RIGHT_ROLE" >/dev/null
done

# Focus tab 1
herdr workspace focus "$WS_ID" >/dev/null 2>&1 || true

echo ""
echo "================================================================================"
echo "  MyCFC Team Workspace successfully configured in Herdr! (Workspace ID: $WS_ID)"
echo "================================================================================"
echo "Tabs & Panes Topology:"
echo "  [1] Delivery Lead & PO : lead (left)            | product-owner (right)"
echo "  [2] Design & UX        : ux-researcher (left)    | product-designer (right)"
echo "  [3] Backend            : backend-domain (left)   | backend-data (right)"
echo "  [4] Frontend           : frontend-ui (left)      | frontend-a11y (right)"
echo "  [5] QA & Security      : qa-engineer (left)      | security-reviewer (right)"
echo "  [6] Ops & Runtime      : devops-sre (left)       | runtime-console (right)"
echo "================================================================================"
echo "Use './scripts/herdr-team-ctl.sh status' to inspect running state."
