variable "subscription_id" {
  description = "The Azure for Students subscription id (az account show --query id -o tsv)."
  type        = string
}

variable "location" {
  description = "Azure region."
  type        = string
  default     = "eastus"
}

variable "name" {
  description = "Prefix for every resource name."
  type        = string
  default     = "gaffer"
}

variable "node_size" {
  description = "Node VM size: the cheapest with the 2 vCPU and 4 GB an AKS node needs."
  type        = string
  default     = "Standard_B2als_v2"
}

variable "node_min" {
  description = "Fewest nodes the cluster autoscaler keeps."
  type        = number
  default     = 1
}

variable "node_max" {
  description = "Most nodes the cluster autoscaler adds."
  type        = number
  default     = 2
}

variable "budget_amount" {
  description = "Monthly budget in USD; alerts at 50% and 80% of it."
  type        = number
  default     = 100
}

variable "alert_email" {
  description = "Where budget alerts go."
  type        = string
}
