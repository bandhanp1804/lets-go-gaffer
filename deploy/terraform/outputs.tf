output "resource_group" {
  value = azurerm_resource_group.main.name
}

output "cluster_name" {
  value = azurerm_kubernetes_cluster.main.name
}

output "get_credentials" {
  description = "Run this to point kubectl at the cluster."
  value       = "az aks get-credentials -g ${azurerm_resource_group.main.name} -n ${azurerm_kubernetes_cluster.main.name} --overwrite-existing"
}

output "acr_login_server" {
  value = azurerm_container_registry.main.login_server
}

output "acr_name" {
  value = azurerm_container_registry.main.name
}

output "postgres_host" {
  value = azurerm_postgresql_flexible_server.main.fqdn
}

output "postgres_user" {
  value = azurerm_postgresql_flexible_server.main.administrator_login
}

output "postgres_password" {
  value     = random_password.postgres.result
  sensitive = true
}

output "ingress_ip" {
  value = azurerm_public_ip.ingress.ip_address
}

output "host" {
  description = "The app's address: the domain, or without one a free sslip.io name for the ingress IP."
  value       = var.domain != "" ? var.domain : local.sslip_host
}

output "legacy_host" {
  description = "The sslip.io name the app used before it had a domain: it now redirects there. Empty without a domain."
  value       = var.domain != "" ? local.sslip_host : ""
}

locals {
  sslip_host = "gaffer.${replace(azurerm_public_ip.ingress.ip_address, ".", "-")}.sslip.io"
}

output "dns_name_servers" {
  description = "Set these as the domain's name servers at the registrar (empty without a domain)."
  value       = var.domain == "" ? [] : azurerm_dns_zone.main[0].name_servers
}
