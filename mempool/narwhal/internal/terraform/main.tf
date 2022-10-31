terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 4.35"
    }
  }
}

provider "google" {
  credentials = file("narwhal-cosmos-6fb9ac2ac84a.json")

  project = var.gcp_project_id
  region  = var.gcp_region
  zone    = var.gcp_zone
}

resource "google_compute_instance_template" "minter_temp" {
  name        = var.template_name
  description = "Template for creating boxes to be used for narwhal testing."

  tags = ["narwhalmint"]

  labels = {
    environment = "dev"
  }

  instance_description = "description assigned to instances"
  machine_type         = var.machine_type
  can_ip_forward       = false

  scheduling {
    preemptible         = false
    automatic_restart   = true
    on_host_maintenance = "MIGRATE"
  }

  // Create a new boot disk from an image
  disk {
    source_image = "debian-cloud/debian-11"
    auto_delete  = true
    boot         = true
  }

  network_interface {
    network = "narwhalmintwork"
    access_config {
      nat_ip = ""
    }
  }

  metadata = {
    grouping = var.group
  }

  metadata_startup_script = <<EOT
    apt update -qq
    apt-get -y --no-install-recommends install zip unzip

    gsutil cp gs://narwhalmint/start.sh /usr/local/lib/start.sh
    gsutil cp gs://narwhalmint/start_services.sh  /usr/local/lib/start_services.sh

    gsutil cp gs://narwhalmint/tendermint.zip /usr/bin/tendermint.zip
    unzip -q -d /usr/bin /usr/bin/tendermint.zip && rm /usr/bin/tendermint.zip

    gsutil cp gs://narwhalmint/narwhal_node.zip /usr/bin/narwhal_node.zip
    unzip -q -d /usr/bin /usr/bin/narwhal_node.zip && rm /usr/bin/narwhal_node.zip

    export BASHRC=/etc/bash.bashrc
    echo 'export IP="$(hostname -i)"' >> "$BASHRC"
    echo 'export TMHOME="/usr/local/lib/tendermint/nodes/$IP"' >> "$BASHRC"
    echo 'export NARHOME="/usr/local/lib/narwhal"' >> "$BASHRC"
    echo 'export NARNODEHOME="/usr/local/lib/narwhal/nodes/$IP"' >> "$BASHRC"
  EOT
}

resource "google_compute_instance_group_manager" "manager" {
  name               = "${var.group_name}-${var.machine_type}-${var.group_target_size}"
  base_instance_name = "narwhalmint"
  target_size        = var.group_target_size
  zone               = var.gcp_zone
  wait_for_instances = true

  timeouts {
    create = "3h"
    delete = "3h"
  }

  version {
    instance_template = google_compute_instance_template.minter_temp.id
  }
}

output "group_name" {
  depends_on = [google_compute_instance_group_manager.manager]
  value      = google_compute_instance_group_manager.manager.name
}

output "group_size" {
  depends_on = [google_compute_instance_group_manager.manager]
  value      = google_compute_instance_group_manager.manager.target_size
}
