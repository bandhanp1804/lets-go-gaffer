# The domain's DNS, served by Azure DNS: the registrar only delegates to the zone's name servers
# (output dns_name_servers), and every record lives here as code. The apex and a wildcard both
# point at the ingress, so the app and each preview (pr-12.<domain>) need no record of their own.
resource "azurerm_dns_zone" "main" {
  count               = var.domain == "" ? 0 : 1
  name                = var.domain
  resource_group_name = azurerm_resource_group.main.name
}

resource "azurerm_dns_a_record" "apex" {
  count               = var.domain == "" ? 0 : 1
  name                = "@"
  zone_name           = azurerm_dns_zone.main[0].name
  resource_group_name = azurerm_resource_group.main.name
  ttl                 = 300
  records             = [azurerm_public_ip.ingress.ip_address]
}

resource "azurerm_dns_a_record" "wildcard" {
  count               = var.domain == "" ? 0 : 1
  name                = "*"
  zone_name           = azurerm_dns_zone.main[0].name
  resource_group_name = azurerm_resource_group.main.name
  ttl                 = 300
  records             = [azurerm_public_ip.ingress.ip_address]
}

resource "azurerm_dns_txt_record" "apex" {
  count               = var.domain != "" && length(var.dns_txt) > 0 ? 1 : 0
  name                = "@"
  zone_name           = azurerm_dns_zone.main[0].name
  resource_group_name = azurerm_resource_group.main.name
  ttl                 = 3600

  dynamic "record" {
    for_each = var.dns_txt
    content {
      value = record.value
    }
  }
}
