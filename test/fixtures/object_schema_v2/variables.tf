terraform {
  required_version = ">= 1.3.0"
}

variable "node_config" {
  type = object({
    machine_type = string
    disk_size_gb = number
    tags         = list(string)
    spot         = bool
  })
}
