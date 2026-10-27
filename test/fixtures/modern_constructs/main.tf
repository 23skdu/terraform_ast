terraform {
  required_version = ">= 1.7.0"
  backend "s3" {
    bucket = "tfstate-prod-bucket"
    key    = "modern_constructs/terraform.tfstate"
    region = "us-east-1"
  }
}

variable "instance_count" {
  type    = number
  default = 2
}

variable "bucket_names" {
  type    = list(string)
  default = ["logs", "metrics"]
}

resource "aws_instance" "workers" {
  count         = var.instance_count
  ami           = "ami-0c55b159cbfafe1f0"
  instance_type = "t3.micro"

  lifecycle {
    prevent_destroy       = true
    create_before_destroy = true
    ignore_changes        = [ami]
  }
}

resource "aws_s3_bucket" "buckets" {
  for_each = toset(var.bucket_names)
  bucket   = each.value
}

moved {
  from = aws_s3_bucket.legacy_logs
  to   = aws_s3_bucket.buckets["logs"]
}

removed {
  from = aws_s3_bucket.retired_archive
  lifecycle {
    destroy = false
  }
}

check "worker_count_positive" {
  assert {
    condition     = var.instance_count > 0
    error_message = "instance_count must be positive."
  }
}
