#!/bin/bash

set -e

# Get database details from Terraform
TERRAFORM_DIR="$(dirname "$0")/../terraform"
cd "$TERRAFORM_DIR"

# Extract database URL from Secrets Manager
SECRET_NAME=$(terraform output -raw ecs_security_group_id 2>/dev/null | xargs -I {} echo "nimbus-prod-database-url" || echo "nimbus-prod-database-url")

echo "Fetching database URL from Secrets Manager..."
DATABASE_URL=$(aws secretsmanager get-secret-value --secret-id "$SECRET_NAME" --region us-east-1 --query SecretString --output text 2>/dev/null || echo "")

if [ -z "$DATABASE_URL" ]; then
  echo "Error: Could not retrieve DATABASE_URL from Secrets Manager"
  echo "Secret name: $SECRET_NAME"
  exit 1
fi

echo "Running migrations via ECS task..."
CLUSTER=$(terraform output -raw ecs_cluster_name)
SUBNET=$(terraform output -json private_subnet_ids | jq -r '.[0]')
SG=$(terraform output -raw ecs_security_group_id)

TASK_ARN=$(aws ecs run-task \
  --cluster "$CLUSTER" \
  --task-definition "nimbus-prod-migrator" \
  --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[$SUBNET],securityGroups=[$SG],assignPublicIp=ENABLED}" \
  --overrides "containerOverrides=[{name=migrator,environment=[{name=DATABASE_URL,value=$DATABASE_URL}]}]" \
  --region us-east-1 \
  --query 'tasks[0].taskArn' \
  --output text 2>/dev/null || echo "")

if [ -z "$TASK_ARN" ] || [ "$TASK_ARN" == "None" ]; then
  echo "Failed to start migration task"
  exit 1
fi

echo "Task ARN: $TASK_ARN"
TASK_ID=$(echo $TASK_ARN | cut -d'/' -f3)

# Wait for task to complete
echo "Waiting for migrations to complete..."
MAX_ATTEMPTS=60
ATTEMPT=0

while [ $ATTEMPT -lt $MAX_ATTEMPTS ]; do
  STATUS=$(aws ecs describe-tasks \
    --cluster "$CLUSTER" \
    --tasks "$TASK_ARN" \
    --region us-east-1 \
    --query 'tasks[0].lastStatus' \
    --output text 2>/dev/null || echo "UNKNOWN")
  
  if [ "$STATUS" == "STOPPED" ]; then
    EXIT_CODE=$(aws ecs describe-tasks \
      --cluster "$CLUSTER" \
      --tasks "$TASK_ARN" \
      --region us-east-1 \
      --query 'tasks[0].containers[0].exitCode' \
      --output text 2>/dev/null || echo "1")
    
    if [ "$EXIT_CODE" -eq 0 ] || [ "$EXIT_CODE" == "0" ]; then
      echo "✓ Migrations completed successfully"
      aws logs tail /ecs/nimbus-migrator --region us-east-1
      exit 0
    else
      echo "✗ Migration failed with exit code $EXIT_CODE"
      echo "Logs:"
      aws logs tail /ecs/nimbus-migrator --region us-east-1
      exit 1
    fi
  fi
  
  echo -n "."
  ATTEMPT=$((ATTEMPT + 1))
  sleep 5
done

echo ""
echo "✗ Migration task timeout"
exit 1
