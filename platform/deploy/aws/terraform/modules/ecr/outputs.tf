output "repo_urls" {
  description = "service name -> repository URI (consumed by push-images.sh and ECS task defs)."
  value = {
    for svc, repo in aws_ecr_repository.this : svc => repo.repository_url
  }
}
