# Nimbus AI Integration

AI-powered orchestration layer for Nimbus notifications using OpenAI's GPT-4o-mini.

## Features

- **Natural Language Interface**: Send notifications using plain English
- **Function Calling**: LLM automatically decides which APIs to call
- **Multi-step Orchestration**: Check status, list notifications, create new ones
- **Production-Ready**: Integrates with real Nimbus APIs and AWS SES

## Architecture

```
User (Natural Language)
        ↓
    LLM Agent (GPT-4o-mini)
        ↓ Function Calling
    Python Orchestration Layer
        ↓ HTTP REST
    Nimbus API
        ↓
    PostgreSQL + AWS SES
```

## Setup

### 1. Prerequisites

- Nimbus running locally (see main [README.md](../README.md))
- OpenAI API key
- AWS SES configured (optional, for real emails)

### 2. Install Dependencies

```bash
cd ai-integration
pip3 install -r requirements.txt
```

### 3. Configure Environment

Add to your `~/.zshrc` or `~/.bashrc`:

```bash
# OpenAI
export OPENAI_API_KEY="your_openai_key_here"

# AWS (optional, for real email delivery)
export AWS_ACCESS_KEY_ID="your_aws_key"
export AWS_SECRET_ACCESS_KEY="your_aws_secret"
export AWS_REGION="us-east-1"
export SES_FROM_EMAIL="your-verified@email.com"
```

Then reload:
```bash
source ~/.zshrc
```

### 4. Start Nimbus

```bash
# From nimbus root directory
go run cmd/gateway/main.go
```

## Usage

### Basic Examples

**Send an email:**
```bash
python3 ai_agent.py --request "Send an email to john@example.com with subject 'Hello' and body 'Test message'"
```

**Check notification status:**
```bash
python3 ai_agent.py --request "Check the status of notification <uuid>"
```

**List recent notifications:**
```bash
python3 ai_agent.py --request "List all recent notifications for the default tenant"
```

### Advanced Examples

**Multi-step orchestration:**
```bash
python3 ai_agent.py --request "Send an email to alice@test.com, then check if it was delivered"
```

**Natural language queries:**
```bash
python3 ai_agent.py --request "How many emails were sent in the last hour?"
```

**Conditional logic:**
```bash
python3 ai_agent.py --request "If there are more than 5 pending notifications, list them"
```

## How It Works

### 1. Function Calling

The LLM has access to three tools:

- `create_notification()` - Send email/SMS/webhook
- `get_notification_status()` - Check delivery status
- `list_notifications()` - Query recent notifications

### 2. Orchestration Flow

```python
User: "Send email to john and check status"
  ↓
LLM: Decides to call create_notification()
  ↓
Python: Executes HTTP POST to Nimbus API
  ↓
LLM: Receives notification ID, decides to call get_notification_status()
  ↓
Python: Executes HTTP GET to Nimbus API
  ↓
LLM: Synthesizes final response
  ↓
User: "Email sent successfully, status: pending"
```

### 3. Why This Architecture?

- **Separation of Concerns**: Nimbus stays generic, AI layer is thin
- **Flexibility**: Can orchestrate multiple services (not just Nimbus)
- **Production Pattern**: Same approach used by ChatGPT plugins, Copilot agents
- **Reusable**: Nimbus APIs work with or without AI layer

## Demo for Interviews

**Talking Points:**

1. **LLM Integration**: "I integrated GPT-4 with function calling to orchestrate notification workflows"
2. **Real Production Stack**: "Uses real AWS SES for email delivery, PostgreSQL for storage"
3. **Architecture**: "Built as a separate orchestration layer - keeps services decoupled"
4. **Multi-step**: "LLM can chain multiple API calls to fulfill complex requests"

**Quick Demo Script:**

```bash
# Terminal 1: Start Nimbus
go run cmd/gateway/main.go

# Terminal 2: AI commands
python3 ai-integration/ai_agent.py --request "Send a test email to my-email@gmail.com"
python3 ai-integration/ai_agent.py --request "List all notifications"
python3 ai-integration/ai_agent.py --request "How many emails are pending?"
```

## Extending

### Add New Tools

Edit `ai_agent.py` and add to `NIMBUS_TOOLS`:

```python
{
    "type": "function",
    "function": {
        "name": "your_function",
        "description": "What it does",
        "parameters": { ... }
    }
}
```

Then implement the function and add to `execute_function()`.

### Connect to Other Services

The LLM can orchestrate multiple services:

```python
# In execute_function(), add calls to:
- Slack API
- Database queries
- External APIs
- Internal microservices
```

## Cost & Performance

- **OpenAI API**: ~$0.001 per request (gpt-4o-mini)
- **Latency**: 1-3 seconds per request
- **Production Tips**: Cache common queries, rate limit, add retries

## Troubleshooting

**"OPENAI_API_KEY not found"**
- Export the key: `export OPENAI_API_KEY="your_key"`
- Reload shell: `source ~/.zshrc`

**"Nimbus is not running"**
- Start Nimbus: `go run cmd/gateway/main.go`
- Check health: `curl http://localhost:8080/health`

**"Emails stuck in pending"**
- Configure AWS SES credentials
- Verify your email in AWS SES Console
- Check Nimbus logs for SES errors

## References

- [OpenAI Function Calling Docs](https://platform.openai.com/docs/guides/function-calling)
- [AWS SES Setup Guide](../ai-integration/AWS_SES_SETUP.md)
- [Nimbus API Documentation](../docs/API.md)
