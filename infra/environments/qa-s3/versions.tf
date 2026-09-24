terraform {
  required_version = "= 1.15.8"

  backend "s3" {
    key          = "mycfc/qa-s3/terraform.tfstate"
    use_lockfile = true
    encrypt      = true
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.56.0"
    }
  }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = "mycfc"
      Environment = "qa-s3"
      ManagedBy   = "terraform"
      Repository  = "ricardoespsanto/mycfc"
    }
  }
}
