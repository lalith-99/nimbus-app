#!/bin/bash
# Initialize LocalStack with required AWS resources

echo "Creating SQS queues for EchoStream..."

# Create the main message queue (used by gateway)
awslocal sqs create-queue --queue-name echostream-messages

# Create the message events queue (alternative naming)  
awslocal sqs create-queue --queue-name echostream-message-events

# Create the dead letter queue
awslocal sqs create-queue --queue-name echostream-messages-dlq

echo "SQS queues created successfully!"

# List queues to verify
awslocal sqs list-queues
