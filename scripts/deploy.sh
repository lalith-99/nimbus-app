#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
TERRAFORM_DIR="$PROJECT_ROOT/terraform"
AWS_REGION="${AWS_REGION:-us-east-1}"
ENVIRONMENT="${ENVIRONMENT:-prod}"

echo -e "${YELLOW}=== Nimbus Deployment Script ===${NC}"
echo "Project Root: $PROJECT_ROOT"
echo "Region: $AWS_REGION"
echo "Environment: $ENVIRONMENT"
echo ""

# Step 1: Get Terraform outputs
echo -e "${YELLOW}[1/5] Retrieving infrastructure details...${NC}"
cd "$TERRAFORM_DIR"

ECR_REGISTRY=$(terraform output -raw ecr_repository_url | cut -d'/' -f1)
ECR_REPO=$(terraform output -raw ecr_repository_url)
ECS_CLUSTER=$(terraform output -raw ecs_cluster_name)
ECS_SERVICE=$(terraform output -raw ecs_service_name)

echo -e "${GREEN}✓ ECR Registry: $ECR_REGISTRY${NC}"
echo -e "${GREEN}✓ ECR Repo: $ECR_REPO${NC}"
echo -e "${GREEN}✓ ECS Cluster: $ECS_CLUSTER${NC}"
echo -e "${GREEN}✓ ECS Service: $ECS_SERVICE${NC}"
echo ""

# Step 2: Authenticate with ECR
echo -e "${YELLOW}[2/5] Authenticating with Amazon ECR...${NC}"
aws ecr get-login-password --region "$AWS_REGION" | \
  docker login --username AWS --password-stdin "$ECR_REGISTRY"
echo -e "${GREEN}✓ ECR login successful${NC}"
echo ""

# Step 3: Build and push Docker images
echo -e "${YELLOW}[3/5] Building and pushing Docker images...${NC}"
cd "$PROJECT_ROOT"

# Build app image
echo "Building nimbus-gateway..."
docker build \
  --platform linux/amd64 \
  -t "$ECR_REPO:latest" \
  -t "$ECR_REPO:$(git rev-parse --short HEAD)" \
  -f Dockerfile .

echo "Pushing nimbus-gateway..."
docker push "$ECR_REPO:latest"
docker push "$ECR_REPO:$(git rev-parse --short HEAD)"
echo -e "${GREEN}✓ App image pushed${NC}"

# Build migrator image
echo "Building nimbus-migrator..."
docker build \
  --platform linux/amd64 \
  -t "$ECR_REPO-migrator:latest" \
  -t "$ECR_REPO-migrator:$(git rev-parse --short HEAD)" \
  -f Dockerfile.migrator .

echo "Pushing nimbus-migrator..."
docker push "$ECR_REPO-migrator:latest"
docker push "$ECR_REPO-migrator:$(git rev-parse --short HEAD)"
echo -e "${GREEN}✓ Migrator image pushed${NC}"
echo ""

# Step 4: Run database migrations
echo -e "${YELLOW}[4/5] Running database migrations...${NC}"

# Get VPC networking details from Terraform state
cd "$TERRAFORM_DIR"

# Extract private subnet IDs
PRIVATE_SUBNET_1=$(terraform state show 'module.vpc.aws_subnet.private[0]' 2>/dev/null | grep 'id' | head -1 | awk '{print $NF}' | tr -d '"' || echo "")
PRIVATE_SUBNET_2=$(terraform state show 'module.vpc.aws_subnet.private[1]' 2>/dev/null | grep 'id' | head -1 | awk '{print $NF}' | tr -d '"' || echo "")

if [ -z "$PRIVATE_SUBNET_1" ]; then
  echo -e "${RED}✗ Could not extract private subnets from Terraform state${NC}"
  exit 1
fi

SUBNET_IDS="$PRIVATE_SUBNET_1"
if [ -n "$PRIVATE_SUBNET_2" ]; then
  SUBNET_IDS="$PRIVATE_SUBNET_1,$PRIVATE_SUBNET_2"
fi

# Extract ECS security group ID
SECURITY_GROUP_ID=$(terraform state show 'aws_security_group.ecs' 2>/dev/null | grep 'id' | head -1 | awk '{print $NF}' | tr -d '"' || echo "")

if [ -z "$SECURITY_GROUP_ID" ]; then
  echo -e "${YELLOW}Warning: Could not find ECS security group, using ALB security group${NC}"
  SECURITY_GROUP_ID=$(terraform state show 'aws_security_group.alb' 2>/dev/null | grep 'id' | head -1 | awk '{print $NF}' | tr -d '"' || echo "")
fi

echo "Subnets: $SUBNET_IDS"
echo "Security Group: $SECURITY_GROUP_ID"

echo "Registering migrator task definition..."
# Get existing task definition and update image
TASK_DEF_JSON=$(aws ecs describe-task-definition \
  --task-definition "nimbus-migrator" \
  --region "$AWS_REGION" 2>/dev/null || echo "{}")

if [ "$TASK_DEF_JSON" == "{}" ]; then
  echo "Creating new migrator task definition..."
  # Create minimal task definition if it doesn't exist
  aws ecs register-task-definition \
    --family "nimbus-migrator" \
    --network-mode awsvpc \
    --requires-compatibilities FARGATE \
    --cpu 256 \
    --memory 512 \
    --container-definitions "[{
      \"name\": \"migrator\",
      \"image\": \"$ECR_REPO-migrator:latest\",
      \"essential\": true,
      \"logConfiguration\": {
        \"logDriver\": \"awslogs\",
        \"options\": {
          \"awslogs-group\": \"/ecs/nimbus-migrator\",
          \"awslogs-region\": \"$AWS_REGION\",
          \"awslogs-stream-prefix\": \"ecs\"
        }
      }
    }]" \
    --execution-role-arn "$(aws iam get-role --role-name "nimbus-${ENVIRONMENT}-ecs-execution" --query 'Role.Arn' --output text --region "$AWS_REGION")" \
    --region "$AWS_REGION" > /dev/null
fi

echo "Running migrations as ECS task..."
TASK_ARN=$(aws ecs run-task \
  --cluster "$ECS_CLUSTER" \
  --task-definition "nimbus-migrator" \
  --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[${SUBNET_IDS}],securityGroups=[${SECURITY_GROUP_ID}],assignPublicIp=ENABLED}" \
  --region "$AWS_REGION" \
  --query 'tasks[0].taskArn' \
  --output text 2>&1)

if [ -z "$TASK_ARN" ] || [ "$TASK_ARN" == "None" ] || echo "$TASK_ARN" | grep -q "Error\|error"; then
  echo -e "${RED}✗ Failed to start migration task${NC}"
  echo "Error: $TASK_ARN"
  echo ""
  echo "This may be due to:"
  echo "  1. Missing VPC configuration in Terraform state"
  echo "  2. Task definition 'nimbus-migrator' not exists in ECS"
  echo "  3. Invalid security group or subnet IDs"
  echo ""
  echo "Current configuration:"
  echo "  Subnets: $SUBNET_IDS"
  echo "  Security Group: $SECURITY_GROUP_ID"
  echo ""
  echo "You can manually create and run the migrator task with proper VPC config."
  exit 1
fi

echo "Migration task ARN: $TASK_ARN"
TASK_ID=$(echo $TASK_ARN | cut -d'/' -f3)

# Wait for migration to complete
echo "Waiting for migrations to complete (this may take a few minutes)..."
MAX_ATTEMPTS=60
ATTEMPT=0

while [ $ATTEMPT -lt $MAX_ATTEMPTS ]; do
  TASK_STATUS=$(aws ecs describe-tasks \
    --cluster "$ECS_CLUSTER" \
    --tasks "$TASK_ARN" \
    --region "$AWS_REGION" \
    --query 'tasks[0].lastStatus' \
    --output text)
  
  if [ "$TASK_STATUS" == "STOPPED" ]; then
    EXIT_CODE=$(aws ecs describe-tasks \
      --cluster "$ECS_CLUSTER" \
      --tasks "$TASK_ARN" \
      --region "$AWS_REGION" \
      --query 'tasks[0].containers[0].exitCode' \
      --output text)
    
    if [ "$EXIT_CODE" -eq 0 ]; then
      echo -e "${GREEN}✓ Migrations completed successfully${NC}"
      break
    else
      echo -e "${RED}✗ Migrations failed with exit code $EXIT_CODE${NC}"
      # Show logs
      echo "Logs:"
      aws logs tail "/ecs/nimbus-migrator" --follow --max-items 50 --region "$AWS_REGION" 2>/dev/null || true
      exit 1
    fi
  fi
  
  echo -n "."
  ATTEMPT=$((ATTEMPT + 1))
  sleep 5
done

if [ $ATTEMPT -eq $MAX_ATTEMPTS ]; then
  echo -e "${RED}✗ Migration task timeout${NC}"
  exit 1
fi

echo ""

# Step 5: Deploy to ECS
echo -e "${YELLOW}[5/5] Deploying to ECS...${NC}"
aws ecs update-service \
  --cluster "$ECS_CLUSTER" \
  --service "$ECS_SERVICE" \
  --force-new-deployment \
  --region "$AWS_REGION" > /dev/null

echo -e "${GREEN}✓ Deployment initiated${NC}"
echo ""

# Wait for service to stabilize
echo "Waiting for service to reach steady state..."
aws ecs wait services-stable \
  --cluster "$ECS_CLUSTER" \
  --services "$ECS_SERVICE" \
  --region "$AWS_REGION" || true

echo ""
echo -e "${GREEN}=== Deployment Complete ===${NC}"
echo ""
echo "Your app is being deployed. Check status with:"
echo "  aws ecs describe-services --cluster $ECS_CLUSTER --services $ECS_SERVICE --region $AWS_REGION"
echo ""
echo "View logs:"
echo "  aws logs tail /ecs/nimbus-prod --follow --region $AWS_REGION"
