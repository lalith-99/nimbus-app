#!/bin/bash

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
TERRAFORM_DIR="$PROJECT_ROOT/terraform"
AWS_REGION="${AWS_REGION:-us-east-1}"

echo -e "${YELLOW}=== Nimbus Deployment ===${NC}"

# Get Terraform outputs
cd "$TERRAFORM_DIR"
ECR_REPO=$(terraform output -raw ecr_repository_url)
ECS_CLUSTER=$(terraform output -raw ecs_cluster_name)
ECS_SERVICE=$(terraform output -raw ecs_service_name)

echo "ECR: $ECR_REPO"
echo "Cluster: $ECS_CLUSTER"

# Login to ECR
echo -e "\n${YELLOW}[1/3] Authenticating with ECR...${NC}"
aws ecr get-login-password --region "$AWS_REGION" | \
  docker login --username AWS --password-stdin "$(echo $ECR_REPO | cut -d'/' -f1)"

# Build and push
echo -e "\n${YELLOW}[2/3] Building and pushing image...${NC}"
cd "$PROJECT_ROOT"
docker build --platform linux/amd64 -t "$ECR_REPO:latest" -t "$ECR_REPO:$(git rev-parse --short HEAD)" .
docker push "$ECR_REPO:latest"
docker push "$ECR_REPO:$(git rev-parse --short HEAD)"

# Deploy
echo -e "\n${YELLOW}[3/3] Deploying to ECS...${NC}"
aws ecs update-service --cluster "$ECS_CLUSTER" --service "$ECS_SERVICE" --force-new-deployment --region "$AWS_REGION" > /dev/null

echo -e "\n${GREEN}✓ Deployment initiated${NC}"
echo ""
echo "To run migrations (if schema changed):"
echo "  cd terraform && ./run-migration.sh"
