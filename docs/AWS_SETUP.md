# AWS Credentials Setup for GitHub Actions

The CI/CD pipeline can run without AWS credentials (tests and builds will work), but to enable Docker push, migrations, and deployments, you need to set up AWS authentication.

## Without AWS (Tests and Build Only)

If you don't have AWS configured, the pipeline will:
- Run tests
- Build binaries
- Build Docker images locally
- Skip Docker push, migrations, and deployments (these require AWS)

## Setting Up AWS OIDC

This is the recommended way to authenticate GitHub Actions with AWS.

### 1. Create OIDC Provider in AWS

```bash
aws iam create-open-id-connect-provider \
  --url https://token.actions.githubusercontent.com \
  --client-id-list sts.amazonaws.com \
  --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1
```

### 2. Create IAM Role for GitHub

Create a role with a trust policy that allows your GitHub repository to assume it. See `trust-policy.json` in the repo root for the template.

```bash
aws iam create-role \
  --role-name github-nimbus-role \
  --assume-role-policy-document file://trust-policy.json
```

### 3. Attach Permissions

The role needs access to ECR, ECS, RDS, Secrets Manager, and CloudWatch Logs. See `ecr-policy.json` in the repo root, then attach it:

```bash
aws iam put-role-policy \
  --role-name github-nimbus-role \
  --policy-name nimbus-ecr-ecs-policy \
  --policy-document file://ecr-policy.json
```

### 4. Add GitHub Secret

1. Go to your repo Settings > Secrets and variables > Actions
2. Create a new repository secret named `AWS_ROLE_ARN`
3. Set its value to the ARN of the role created above

### 5. Test

```bash
git push origin main
```

Check the Actions tab to confirm the pipeline runs end-to-end.

## Troubleshooting

### "Credentials could not be loaded"

**Cause:** `AWS_ROLE_ARN` secret not set in GitHub.
**Fix:** Add the secret as described in step 4.

### "Access Denied: User is not authorized"

**Cause:** IAM policy missing required permissions.
**Fix:** Update the policy to include all needed actions.

### "ECR repository not found"

**Cause:** Repository doesn't exist.
**Fix:**
```bash
aws ecr create-repository \
  --repository-name nimbus-prod \
  --region us-east-1
```

### "ECS cluster not found"

**Cause:** Infrastructure not provisioned.
**Fix:** Run `terraform apply` from the `terraform/` directory first.