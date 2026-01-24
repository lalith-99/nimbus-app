# AWS Credentials Setup for GitHub Actions

The CI/CD pipeline can run without AWS credentials (tests and builds will work), but to enable Docker push, migrations, and deployments, you need to set up AWS authentication.

## Option 1: Quick Start (No AWS - Tests & Build Only)

If you don't have AWS configured yet, the pipeline will:
- ✅ Run tests
- ✅ Build binaries  
- ✅ Build Docker images locally
- ⏭️ Skip: Docker push, migrations, deployments (these require AWS)

Just push your code and it will work!

## Option 2: Set Up AWS OIDC (Recommended)

This is the secure way to authenticate GitHub Actions with AWS without storing credentials.

### Step 1: Create OIDC Provider in AWS

✅ **Already done!**

OIDC Provider ARN: `arn:aws:iam::077711495301:oidc-provider/token.actions.githubusercontent.com`

If you need to recreate it:

```bash
aws iam create-open-id-connect-provider \
  --url https://token.actions.githubusercontent.com \
  --client-id-list sts.amazonaws.com \
  --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1
```

### Step 2: Create IAM Role for GitHub

✅ **Already done!**

Role Name: `github-nimbus-role`
Role ARN: `arn:aws:iam::077711495301:role/github-nimbus-role`

The role was created with trust policy for:
- Repository: `lalith-99/nimbus-app`
- Account ID: `077711495301`

To verify:
```bash
aws iam get-role --role-name github-nimbus-role
```

### Step 3: Attach Permissions

```bash
# Create policy for ECR, ECS, RDS, Secrets Manager
cat > ecr-policy.json << 'EOF'
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ecr:GetAuthorizationToken",
        "ecr:BatchGetImage",
        "ecr:GetDownloadUrlForLayer",
        "ecr:PutImage",
        "ecr:InitiateLayerUpload",
        "ecr:UploadLayerPart",
        "ecr:CompleteLayerUpload",
        "ecr:DescribeImages",
        "ecr:DescribeRepositories"
      ],
      "Resource": "arn:aws:ecr:*:YOUR_ACCOUNT_ID:repository/nimbus-prod"
    },
    {
      "Effect": "Allow",
      "Action": [
✅ **Already done!**

Policy Name: `nimbus-ecr-ecs-policy`

The policy grants access to:
- **ECR**: Push/pull Docker images to `nimbus-prod` repository
- **ECS**: Deploy to `nimbus-prod` cluster and service
- **RDS**: Read database instances
- **Secrets Manager**: Read database credentials
- **CloudWatch Logs**: Write deployment logs

To verify:
```bash
aws iam get-role-policy --role-name github-nimbus-role --policy-name nimbus-ecr-ecs-policy
- ✅ Deploy to ECS

## Troubleshooting

### "Credentials could not be loaded"

**Cause:** AWS_ROLE_ARN secret not set
**Fix:** Add the secret to GitHub as described in Step 4

### "Access Denied: User is not authorized"

**Cause:** IAM policy doesn't have required permissions
**Fix:** Update the policy to include all needed actions

### "ECR repository not found"

**Cause:** Repository doesn't exist
**Fix:** Create ECR repository:
```bash
aws ecr create-repository \
  --repository-name nimbus-prod \
  --region us-east-1
```

### "ECS cluster not found"

**Cause:** Cluster not created
**Fix:** Create ECS cluster first (see ECS setup)

## Verification

Check if credentials are properly configured:

```bash
# List GitHub secrets (shows names only)
gh secret list
⏳ **You need to do this manually in GitHub**

1. Go to: https://github.com/lalith-99/nimbus-app/settings/secrets/actions
2. Click "New repository secret"
3. **Name:** `AWS_ROLE_ARN`
4. **Value:** `arn:aws:iam::077711495301:role/github-nimbus-role`
5. Click "Add secret"
Add Secret and Test

After adding the secret to GitHub:

```bash
git push origin main
```

Visit: https://github.com/lalith-99/nimbus-app/actions

The workflow should now:
- ✅ Run tests
- ✅ Build binaries
- ✅ Build & push Docker to ECR (`077711495301.dkr.ecr.us-east-1.amazonaws.com/nimbus-prod`)s://docs.aws.amazon.com/ecr/)
Status:** ✅ Repository already created at `077711495301.dkr.ecr.us-east-1.amazonaws.com/nimbus-prod