#!/bin/sh
set -eu

cloudflare_ips=https://api.cloudflare.com/client/v4/ips
interface=$(ip -o route show default | awk '{print $5; exit}')

if [ -z "$interface" ]; then
	printf '%s\n' 'Unable to determine the public network interface.' >&2
	exit 1
fi

apply_rules() {
	tool=$1
	filter=$2
	chain=MYCFC_EDGE

	"$tool" -N "$chain" 2>/dev/null || true
	"$tool" -F "$chain"
	if ! "$tool" -C DOCKER-USER -j "$chain" 2>/dev/null; then
		"$tool" -I DOCKER-USER -j "$chain"
	fi

	"$tool" -A "$chain" -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
	for cidr in $(curl --fail --silent --show-error "$cloudflare_ips" | jq -r "$filter"); do
		"$tool" -A "$chain" -i "$interface" -p tcp -s "$cidr" --dport 80 -j RETURN
		"$tool" -A "$chain" -i "$interface" -p tcp -s "$cidr" --dport 443 -j RETURN
	done
	"$tool" -A "$chain" -i "$interface" -p tcp --dport 80 -j DROP
	"$tool" -A "$chain" -i "$interface" -p tcp --dport 443 -j DROP
	"$tool" -A "$chain" -j RETURN
}

apply_rules iptables '.result.ipv4_cidrs[]'
apply_rules ip6tables '.result.ipv6_cidrs[]'
