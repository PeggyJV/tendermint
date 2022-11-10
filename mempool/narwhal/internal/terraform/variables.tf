variable "gcp_project_id" {
  type    = string
  default = "narwhal-cosmos"
}

variable "gcp_region" {
  type    = string
  default = "us-central1"
}

variable "gcp_zone" {
  type    = string
  default = "us-central1-a"
}

variable "template_name" {
  type    = string
  default = "narwhalmintempl"
}

variable "group" {
  type    = string
  default = "default"
}

variable "group_name" {
  type    = string
  default = "minters"
}

variable "group_target_size" {
  type    = string
  default = "4"
}

variable "machine_type" {
  type    = string
  default = "e2-standard-8"
}

variable "narwhal_binary" {
  type    = string
  default = "narwhal_node.zip"
}
