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
  # State lives in an Azure storage account (deploy/scripts/ci-setup.sh made it), so the laptop and
  # GitHub Actions share it and take turns through its lock. Names are in backend.hcl; access is
  # by Entra ID, not storage keys.
  backend "azurerm" {}
}

provider "azurerm" {
  features {}
  subscription_id = var.subscription_id
}
