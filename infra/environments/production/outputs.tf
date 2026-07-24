output "vpc_id" {
  value = aws_vpc.this.id
}

output "repair_bucket_name" {
  value = aws_s3_bucket.repairs.bucket
}

output "ecr_repository_url" {
  value = aws_ecr_repository.app.repository_url
}

output "rds_endpoint" {
  value = aws_db_instance.postgres.address
}
