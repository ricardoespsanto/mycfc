output "server_id" {
  value = hcloud_server.app.id
}

output "ipv4_address" {
  value = hcloud_server.app.ipv4_address
}

output "ipv6_address" {
  value = hcloud_server.app.ipv6_address
}
