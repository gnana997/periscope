# Minimal, free-tier-friendly EC2 instance that registers with SSM, so
# the POC probe has a real target. Scope is deliberately tiny: default
# VPC, a public subnet, no inbound rules (SSM is outbound-only), and the
# single AWS-managed policy that lets the SSM agent register.
#
# This is NOT the production IAM model. There is no per-user role and no
# OIDC trust policy here — auth is stubbed by ambient credentials (see
# ../README.md). The per-user sts:AssumeRoleWithWebIdentity role lands
# when the production feature is built.

terraform {
  required_version = ">= 1.3"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    # Only used to fetch the OIDC issuer's cert thumbprint when
    # enable_oidc=true (see iam.tf).
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
}

data "aws_caller_identity" "current" {}

provider "aws" {
  region = var.region
}

# Latest Amazon Linux 2023 AMI for x86_64 (t2.micro is x86_64). AL2023
# ships the SSM agent preinstalled and enabled — no user_data needed.
data "aws_ssm_parameter" "al2023" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
}

# Use the account's default VPC + one of its (public) default subnets.
data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "default" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }
}

# --- IAM: let the SSM agent register and talk to Session Manager ---
data "aws_iam_policy_document" "assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "ssm" {
  name               = "${var.name}-role"
  assume_role_policy = data.aws_iam_policy_document.assume.json
  tags               = local.tags
}

resource "aws_iam_role_policy_attachment" "ssm_core" {
  role       = aws_iam_role.ssm.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "ssm" {
  name = "${var.name}-profile"
  role = aws_iam_role.ssm.name
  tags = local.tags
}

# --- Network: outbound-only. SSM needs no inbound (no SSH, no ports). ---
resource "aws_security_group" "ssm" {
  name        = "${var.name}-sg"
  description = "POC SSM target - egress only, no inbound"
  vpc_id      = data.aws_vpc.default.id

  egress {
    description = "all outbound (SSM endpoints over 443)"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = local.tags
}

resource "aws_instance" "node" {
  ami                         = data.aws_ssm_parameter.al2023.value
  instance_type               = var.instance_type
  iam_instance_profile        = aws_iam_instance_profile.ssm.name
  subnet_id                   = tolist(data.aws_subnets.default.ids)[0]
  vpc_security_group_ids      = [aws_security_group.ssm.id]
  associate_public_ip_address = true

  metadata_options {
    http_tokens = "required" # IMDSv2 only
  }

  tags = local.tags
}

locals {
  tags = {
    Name      = var.name
    Project   = "periscope"
    Purpose   = "issue-105-ssm-node-shell-poc"
    ManagedBy = "terraform"
  }
}
