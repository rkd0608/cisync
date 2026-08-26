###############################################################################
# iam: execution role (pull images, write logs, RESOLVE SECRETS at task start)
# + one shared minimal task role. Services speak HTTP/PG only — no AWS SDK
# calls exist in the Go code today, so the task role grants NOTHING; it exists
# as the B6 KMS graduation seam (ledger key custody) and future S3 artifacts.
###############################################################################

data "aws_iam_policy_document" "execution_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "execution" {
  name               = "${var.name_prefix}-ecs-execution"
  assume_role_policy = data.aws_iam_policy_document.execution_assume.json
}

resource "aws_iam_role_policy_attachment" "execution_managed" {
  # Managed policy covers ECR pull + CloudWatch logs. Secret reads are added
  # inline below scoped to THIS kit's secret ARNs only.
  role       = aws_iam_role.execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

data "aws_iam_policy_document" "execution_secrets" {
  statement {
    sid       = "ReadCISyncRuntimeSecrets"
    effect    = "Allow"
    actions   = ["secretsmanager:GetSecretValue", "ssm:GetParameters"]
    resources = values(var.secret_arns)
  }

  statement {
    sid       = "KmsDecryptManagedSecrets"
    effect    = "Allow"
    actions   = ["kms:Decrypt"]
    resources = ["*"] # service key only; scoped by GetSecretValue above

    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["secretsmanager.${var.region}.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "execution_secrets" {
  name   = "read-cisync-secrets"
  role   = aws_iam_role.execution.id
  policy = data.aws_iam_policy_document.execution_secrets.json
}

resource "aws_iam_role" "task" {
  name               = "${var.name_prefix}-ecs-task"
  assume_role_policy = data.aws_iam_policy_document.execution_assume.json
}
