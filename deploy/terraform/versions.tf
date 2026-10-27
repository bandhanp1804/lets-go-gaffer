terraform {
  required_version = ">= 1.9"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "= 5.9.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "= 3.9.1"
    }
  }
  # Local state: one person, one machine. It holds the database password, so it is gitignored.
}

provider "azurerm" {
  features {}
  subscription_id = var.subscription_id
}
