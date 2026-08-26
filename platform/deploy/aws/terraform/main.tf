###############################################################################
# Sauron AWS kit — module wiring. One call per concern; data flows left->right:
# network -> (alb, ecs) ; ecr -> ecs ; secrets -> ecs ; ecs.sg -> rds ingress.
# No third-party modules (native-first charter).
###############################################################################

locals {
  name_prefix = "sauron-${var.environment}"
}

module "network" {
  source             = "./modules/network"
  name_prefix        = local.name_prefix
  vpc_cidr           = var.vpc_cidr
  az_count           = 2 # ARCHITECTURE v0.2: exactly two AZs
  single_nat_gateway = var.single_nat_gateway
}

module "ecr" {
  source      = "./modules/ecr"
  name_prefix = local.name_prefix
}

module "secrets" {
  source      = "./modules/secrets"
  environment = var.environment
}

module "alb" {
  source              = "./modules/alb"
  name_prefix         = local.name_prefix
  vpc_id              = module.network.vpc_id
  public_subnet_ids   = module.network.public_subnet_ids
  certificate_arn     = var.certificate_arn
  allowed_cidrs       = var.allowed_admin_cidrs
  domain_name         = var.domain_name
  hosted_zone_id      = var.hosted_zone_id
  ingest_service_port = 8080
  web_service_port    = 3000
}

module "ecs" {
  source = "./modules/ecs"

  name_prefix            = local.name_prefix
  environment            = var.environment
  region                 = var.aws_region
  vpc_id                 = module.network.vpc_id
  private_subnet_ids     = module.network.private_subnet_ids
  alb_sg_id              = module.alb.alb_sg_id
  image_tag              = var.image_tag
  enable_services        = var.enable_services
  keystore_image         = var.keystore_image
  log_retention_days     = var.log_retention_days
  tracked_base_branches  = var.tracked_base_branches
  connector_details_url  = var.connector_details_url != "" ? var.connector_details_url : (var.domain_name != "" ? "https://${var.domain_name}" : "")
  connector_live_enabled = var.enable_connector_live_mode
  github_app_id          = var.github_app_id
  github_installation_id = var.github_installation_id

  ecr_urls    = module.ecr.repo_urls
  secret_arns = module.secrets.secret_arns

  # ALB wiring: target groups + the SG that may reach task ports.
  ingest_target_group_arn = module.alb.ingest_target_group_arn
  web_target_group_arn    = module.alb.web_target_group_arn

  depends_on = [module.network] # subnets/SGs must exist before task ENIs
}

module "rds" {
  source = "./modules/rds"

  name_prefix            = local.name_prefix
  vpc_id                 = module.network.vpc_id
  private_subnet_ids     = module.network.private_subnet_ids
  instance_class         = var.db_instance_class
  engine_version         = var.db_engine_version
  allocated_storage      = var.db_allocated_storage
  max_allocated_storage  = var.db_max_allocated_storage
  multi_az               = var.db_multi_az
  backup_retention_days  = var.db_backup_retention_days
  deletion_protection    = var.db_deletion_protection
  skip_final_snapshot    = var.db_skip_final_snapshot
  allowed_ingress_sg_ids = [module.ecs.ecs_tasks_sg_id]

  depends_on = [module.ecs] # only to break sg-id chicken/egg ordering in plan UX
}
