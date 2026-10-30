resource "azurerm_resource_group" "main" {
  name     = "${var.name}-rg"
  location = var.location
}

# One network: AKS nodes in one subnet, Postgres in a delegated one, so the database has no
# public address and only the cluster can reach it.
resource "azurerm_virtual_network" "main" {
  name                = "${var.name}-vnet"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  address_space       = ["10.40.0.0/16"]
}

resource "azurerm_subnet" "aks" {
  name                 = "aks"
  resource_group_name  = azurerm_resource_group.main.name
  virtual_network_name = azurerm_virtual_network.main.name
  address_prefixes     = ["10.40.0.0/22"]
}

resource "azurerm_subnet" "postgres" {
  name                 = "postgres"
  resource_group_name  = azurerm_resource_group.main.name
  virtual_network_name = azurerm_virtual_network.main.name
  address_prefixes     = ["10.40.8.0/24"]
  # Azure adds this endpoint itself when it sets up the server; declaring it stops a plan from
  # trying to remove it on every run.
  service_endpoint {
    service = "Microsoft.Storage"
  }
  delegation {
    name = "postgres"
    service_delegation {
      name    = "Microsoft.DBforPostgreSQL/flexibleServers"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }
}

resource "azurerm_private_dns_zone" "postgres" {
  name                = "${var.name}.private.postgres.database.azure.com"
  resource_group_name = azurerm_resource_group.main.name
}

resource "azurerm_private_dns_zone_virtual_network_link" "postgres" {
  name                = "${var.name}-postgres"
  private_dns_zone_id = azurerm_private_dns_zone.postgres.id
  virtual_network_id  = azurerm_virtual_network.main.id
}

# The cluster: free control plane, one node pool that the cluster autoscaler grows from
# node_min to node_max. Pods get overlay addresses, so the subnet only needs room for nodes.
resource "azurerm_kubernetes_cluster" "main" {
  name                = "${var.name}-aks"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  dns_prefix          = var.name
  sku_tier            = "Free"
  oidc_issuer_enabled = true

  default_node_pool {
    name                        = "system"
    vm_size                     = var.node_size
    auto_scaling_enabled        = true
    min_count                   = var.node_min
    max_count                   = var.node_max
    os_disk_size_gb             = 32
    vnet_subnet_id              = azurerm_subnet.aks.id
    temporary_name_for_rotation = "rotate"
    upgrade_settings {
      max_surge = "1"
    }
  }

  identity {
    type = "SystemAssigned"
  }

  # Nodes come from the pool above and its autoscaler, not from node auto-provisioning.
  node_provisioning_profile {
    mode = "Manual"
  }

  network_profile {
    network_plugin      = "azure"
    network_plugin_mode = "overlay"
    pod_cidr            = "192.168.0.0/16"
    service_cidr        = "10.41.0.0/16"
    dns_service_ip      = "10.41.0.10"
    load_balancer_sku   = "standard"
  }

  lifecycle {
    # The autoscaler moves the node count; Terraform should not move it back.
    ignore_changes = [default_node_pool[0].node_count]
  }
}

# The ingress address, fixed for the life of the cluster: the domain's records, the old sslip.io
# name and the sign-in callbacks all depend on it, so stopping and starting the cluster must not change it. It sits in
# the node resource group, where the cluster may already attach it to its load balancer.
resource "azurerm_public_ip" "ingress" {
  name                = "${var.name}-ingress"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_kubernetes_cluster.main.node_resource_group
  allocation_method   = "Static"
  sku                 = "Standard"
}

# Images live in a registry the cluster can pull from with its own identity: no pull secret.
resource "random_string" "acr" {
  length  = 6
  upper   = false
  special = false
}

resource "azurerm_container_registry" "main" {
  name                = "${var.name}${random_string.acr.result}"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  sku                 = "Standard" # free for 12 months on Azure for Students
  admin_enabled       = false
}

resource "azurerm_role_assignment" "aks_pull" {
  scope                            = azurerm_container_registry.main.id
  role_definition_name             = "AcrPull"
  principal_id                     = azurerm_kubernetes_cluster.main.kubelet_identity[0].object_id
  skip_service_principal_aad_check = true
}

# Postgres: the Burstable B1ms tier with 32 GB is free for 12 months on Azure for Students.
resource "random_password" "postgres" {
  length  = 32
  special = false
}

resource "azurerm_postgresql_flexible_server" "main" {
  name                          = "${var.name}-pg-${random_string.acr.result}"
  location                      = azurerm_resource_group.main.location
  resource_group_name           = azurerm_resource_group.main.name
  version                       = "16"
  sku_name                      = "B_Standard_B1ms"
  storage_mb                    = 32768
  storage_tier                  = "P4"
  backup_retention_days         = 7
  delegated_subnet_id           = azurerm_subnet.postgres.id
  private_dns_zone_id           = azurerm_private_dns_zone.postgres.id
  public_network_access_enabled = false
  administrator_login           = "gaffer"
  administrator_password        = random_password.postgres.result

  depends_on = [azurerm_private_dns_zone_virtual_network_link.postgres]

  lifecycle {
    # Azure picks an availability zone; do not fight it.
    ignore_changes = [zone]
  }
}

# pgvector for the news search, btree_gin for Temporal's visibility schema.
resource "azurerm_postgresql_flexible_server_configuration" "extensions" {
  name      = "azure.extensions"
  server_id = azurerm_postgresql_flexible_server.main.id
  value     = "VECTOR,BTREE_GIN"
}

resource "azurerm_postgresql_flexible_server_database" "db" {
  for_each  = toset(["gaffer", "temporal", "temporal_visibility"])
  name      = each.key
  server_id = azurerm_postgresql_flexible_server.main.id
  charset   = "UTF8"
  collation = "en_US.utf8"
}

# Alerts before the credit runs out.
data "azurerm_subscription" "current" {}

resource "azurerm_consumption_budget_subscription" "main" {
  name            = "${var.name}-budget"
  subscription_id = data.azurerm_subscription.current.id
  amount          = var.budget_amount
  time_grain      = "Monthly"

  time_period {
    start_date = formatdate("YYYY-MM-01'T'00:00:00Z", timestamp())
  }

  # Emails at half, most and all of the budget actually spent, and as soon as Azure forecasts the
  # month will go over: that one arrives days earlier, while there is still time to stop the
  # cluster (deploy/scripts/stop.sh).
  dynamic "notification" {
    for_each = [
      { threshold = 50, type = "Actual" },
      { threshold = 80, type = "Actual" },
      { threshold = 100, type = "Actual" },
      { threshold = 100, type = "Forecasted" },
    ]
    content {
      enabled        = true
      threshold      = notification.value.threshold
      operator       = "GreaterThanOrEqualTo"
      threshold_type = notification.value.type
      contact_emails = [var.alert_email]
    }
  }

  lifecycle {
    ignore_changes = [time_period]
  }
}
