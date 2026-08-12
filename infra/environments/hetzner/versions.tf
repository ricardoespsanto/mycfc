terraform {
  required_version = "= 1.15.8"

  backend "s3" {
    key          = "mycfc/hetzner/terraform.tfstate"
    use_lockfile = true
    encrypt      = true
  }

  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "= 1.68.0"
    }
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.56.0"
    }
  }
}

provider "hcloud" {}

provider "aws" {
  region = "eu-west-1"
}
