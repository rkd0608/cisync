###############################################################################
# network: VPC across 2 AZs; public subnets (ALB, NAT) + private subnets
# (ECS tasks, RDS). NAT is the dominant fixed cost — single_nat_gateway
# toggles 1 vs per-AZ gateways.
###############################################################################

resource "aws_vpc" "this" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true # required for CloudMap private DNS + RDS hostnames
  enable_dns_hostnames = true # required for VPC-interface endpoints & RDS
}

# One /20 per AZ: ~4096 IPs each — far beyond v0.2 task counts, roomy for W3+.
resource "aws_subnet" "public" {
  count                   = var.az_count
  vpc_id                  = aws_vpc.this.id
  availability_zone       = data.aws_availability_zones.available.names[count.index]
  cidr_block              = cidrsubnet(var.vpc_cidr, 4, count.index)
  map_public_ip_on_launch = true
  tags                    = { Name = "${var.name_prefix}-public-${count.index + 1}" }
}

resource "aws_subnet" "private" {
  count             = var.az_count
  vpc_id            = aws_vpc.this.id
  availability_zone = data.aws_availability_zones.available.names[count.index]
  cidr_block        = cidrsubnet(var.vpc_cidr, 4, count.index + 8)
  tags              = { Name = "${var.name_prefix}-private-${count.index + 1}" }
}

data "aws_availability_zones" "available" {
  state = "available"
}

resource "aws_internet_gateway" "this" {
  tags = { Name = "${var.name_prefix}-igw" }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }
  tags = { Name = "${var.name_prefix}-public-rt" }
}

resource "aws_route_table_association" "public" {
  count          = var.az_count
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# --- NAT (COST FLAGGED) -------------------------------------------------------
# EIP-per-NAT is required by AWS; single gateway shares one EIP.

resource "aws_eip" "nat" {
  count  = var.single_nat_gateway ? 1 : var.az_count
  domain = "vpc"
  tags   = { Name = "${var.name_prefix}-nat-${count.index + 1}" }
}

resource "aws_nat_gateway" "this" {
  count         = var.single_nat_gateway ? 1 : var.az_count
  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id # single mode: AZ0 hosts it
  tags          = { Name = "${var.name_prefix}-nat-${count.index + 1}" }
  depends_on    = [aws_internet_gateway.this]
}

resource "aws_route_table" "private" {
  count  = var.az_count
  vpc_id = aws_vpc.this.id
  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.this[var.single_nat_gateway ? 0 : count.index].id
  }
  tags = { Name = "${var.name_prefix}-private-rt-${count.index + 1}" }
}

resource "aws_route_table_association" "private" {
  count          = var.az_count
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[count.index].id
}
