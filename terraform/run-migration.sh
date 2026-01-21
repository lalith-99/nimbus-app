#!/bin/bash
# Run database migrations as ECS Fargate task

set -e

CLUSTER=$(terraform output -raw ecs_cluster_name)
SUBNET=$(terraform output -json private_subnet_ids | jq -r '.[0]')
SG=$(terraform output -raw ecs_security_group_id)

echo "Running migrations..."
TASK_ARN=$(aws ecs run-task \
  --cluster "$CLUSTER" \
  --task-definition "nimbus-prod-migrator" \
  --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[$SUBNET],securityGroups=[$SG],assignPublicIp=ENABLED}" \
  --region us-east-1 \
  --query 'tasks[0].taskArn' \
  --output text)

echo "Task: $TASK_ARN"
echo "Waiting for completion..."

# Wait for task to stop
aws ecs wait tasks-stopped --cluster "$CLUSTER" --tasks "$TASK_ARN" --region us-east-1

# Check exit code
EXIT_CODE=$(aws ecs describe-tasks --cluster "$CLUSTER" --tasks "$TASK_ARN" --region us-east-1 \
  --query 'tasks[0].containers[0].exitCode' --output text)

if [ "$EXIT_CODE" == "0" ]; then
  echo "✓ Migrations completed successfully"
  aws logs tail /ecs/nimbus-migrator --region us-east-1 | tail -10
else
  echo "✗ Migrations failed (exit code: $EXIT_CODE)"
  aws logs tail /ecs/nimbus-migrator --region us-east-1 | tail -20
  exit 1
fi
