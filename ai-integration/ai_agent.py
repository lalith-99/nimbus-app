"""
LLM Function Calling + Nimbus Integration

This shows how to wire LLM function calling to real Nimbus APIs.
The LLM orchestrates multiple API calls based on natural language requests.

Example:
  "Send an email to john@example.com and check if it was delivered"
  -> LLM calls create_notification + get_notification_status
"""

import os
import json
import requests
from typing import Dict, Any, List, Optional
from openai import OpenAI


# Nimbus API base URL (assumes Nimbus is running locally)
NIMBUS_BASE_URL = os.getenv("NIMBUS_BASE_URL", "http://localhost:8080")


# ============================================================
# Tool implementations (these call real Nimbus APIs)
# ============================================================


def create_notification(
    tenant_id: str,
    user_id: str,
    channel: str,
    recipient: str,
    subject: Optional[str] = None,
    body: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Create a notification in Nimbus.
    
    Args:
        tenant_id: UUID of the tenant
        user_id: UUID of the user
        channel: 'email', 'sms', or 'webhook'
        recipient: Email address, phone number, or webhook URL
        subject: Email subject (optional)
        body: Notification body/message
    
    Returns:
        {"notification_id": "uuid", "status": "created"}
    """
    payload = {"to": recipient}
    if subject:
        payload["subject"] = subject
    if body:
        payload["body"] = body
    
    data = {
        "tenant_id": tenant_id,
        "user_id": user_id,
        "channel": channel,
        "payload": payload,
    }
    
    try:
        resp = requests.post(
            f"{NIMBUS_BASE_URL}/v1/notifications",
            json=data,
            timeout=5,
        )
        resp.raise_for_status()
        result = resp.json()
        return {
            "notification_id": result.get("id"),
            "status": "created",
            "message": f"{channel.capitalize()} notification created successfully",
        }
    except requests.RequestException as e:
        return {"error": str(e), "status": "failed"}


def get_notification_status(notification_id: str) -> Dict[str, Any]:
    """
    Get the status of a notification.
    
    Args:
        notification_id: UUID of the notification
    
    Returns:
        {"id": "uuid", "status": "pending|delivered|failed", "channel": "email"}
    """
    try:
        resp = requests.get(
            f"{NIMBUS_BASE_URL}/v1/notifications/{notification_id}",
            timeout=5,
        )
        resp.raise_for_status()
        notif = resp.json()
        return {
            "id": notif.get("id"),
            "status": notif.get("status", "unknown"),
            "channel": notif.get("channel"),
            "created_at": notif.get("created_at"),
        }
    except requests.RequestException as e:
        return {"error": str(e), "status": "failed"}


def list_notifications(tenant_id: str, limit: int = 10) -> Dict[str, Any]:
    """
    List notifications for a tenant.
    
    Args:
        tenant_id: UUID of the tenant
        limit: Maximum number of notifications to return (default 10)
    
    Returns:
        {"notifications": [...], "count": 5}
    """
    try:
        resp = requests.get(
            f"{NIMBUS_BASE_URL}/v1/notifications",
            params={"tenant_id": tenant_id, "limit": limit},
            timeout=5,
        )
        resp.raise_for_status()
        notifs = resp.json()
        return {
            "notifications": notifs,
            "count": len(notifs),
            "tenant_id": tenant_id,
        }
    except requests.RequestException as e:
        return {"error": str(e), "notifications": []}


# ============================================================
# Function definitions for OpenAI (schema)
# ============================================================

NIMBUS_TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "create_notification",
            "description": "Create and send a notification via email, SMS, or webhook through Nimbus",
            "parameters": {
                "type": "object",
                "properties": {
                    "tenant_id": {
                        "type": "string",
                        "description": "Tenant UUID (use default: 00000000-0000-0000-0000-000000000001)",
                    },
                    "user_id": {
                        "type": "string",
                        "description": "User UUID (use default: 00000000-0000-0000-0000-000000000002)",
                    },
                    "channel": {
                        "type": "string",
                        "enum": ["email", "sms", "webhook"],
                        "description": "Notification channel",
                    },
                    "recipient": {
                        "type": "string",
                        "description": "Email address, phone number, or webhook URL",
                    },
                    "subject": {
                        "type": "string",
                        "description": "Email subject (optional, for email channel only)",
                    },
                    "body": {
                        "type": "string",
                        "description": "Notification body/message content",
                    },
                },
                "required": ["tenant_id", "user_id", "channel", "recipient"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_notification_status",
            "description": "Check the delivery status of a notification",
            "parameters": {
                "type": "object",
                "properties": {
                    "notification_id": {
                        "type": "string",
                        "description": "UUID of the notification to check",
                    },
                },
                "required": ["notification_id"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "list_notifications",
            "description": "List recent notifications for a tenant",
            "parameters": {
                "type": "object",
                "properties": {
                    "tenant_id": {
                        "type": "string",
                        "description": "Tenant UUID",
                    },
                    "limit": {
                        "type": "integer",
                        "description": "Maximum number of notifications to return (default 10)",
                        "default": 10,
                    },
                },
                "required": ["tenant_id"],
            },
        },
    },
]


# ============================================================
# Function dispatcher
# ============================================================


def execute_function(func_name: str, arguments: Dict[str, Any]) -> Dict[str, Any]:
    """Execute the requested function with parsed arguments."""
    if func_name == "create_notification":
        return create_notification(**arguments)
    elif func_name == "get_notification_status":
        return get_notification_status(**arguments)
    elif func_name == "list_notifications":
        return list_notifications(**arguments)
    else:
        return {"error": f"Unknown function: {func_name}"}


# ============================================================
# LLM orchestration
# ============================================================


def run_agent(user_request: str, max_iterations: int = 5) -> str:
    """
    Run the LLM agent with function calling.
    
    The agent can call Nimbus APIs multiple times to fulfill complex requests.
    
    Args:
        user_request: Natural language request from user
        max_iterations: Max number of function call loops (prevents infinite loops)
    
    Returns:
        Final assistant response
    """
    api_key = os.getenv("OPENAI_API_KEY")
    if not api_key:
        raise SystemExit("Missing OPENAI_API_KEY. Export it first.")
    
    client = OpenAI(api_key=api_key)
    
    messages = [
        {
            "role": "system",
            "content": (
                "You are Nimbus AI, an assistant that manages notifications. "
                "You can create notifications, check their status, and list recent notifications. "
                "Use UUIDs: tenant_id=00000000-0000-0000-0000-000000000001, "
                "user_id=00000000-0000-0000-0000-000000000002 as defaults. "
                "Be concise and friendly."
            ),
        },
        {"role": "user", "content": user_request},
    ]
    
    iteration = 0
    
    while iteration < max_iterations:
        iteration += 1
        
        response = client.chat.completions.create(
            model="gpt-4o-mini",
            messages=messages,
            tools=NIMBUS_TOOLS,
            tool_choice="auto",
            temperature=0.2,
        )
        
        message = response.choices[0].message
        messages.append(message)
        
        # If no tool calls, we're done
        if not message.tool_calls:
            return message.content
        
        # Execute each tool call
        for tool_call in message.tool_calls:
            func_name = tool_call.function.name
            arguments = json.loads(tool_call.function.arguments)
            
            print(f"\n🔧 Calling: {func_name}")
            print(f"   Args: {json.dumps(arguments, indent=2)}")
            
            result = execute_function(func_name, arguments)
            
            print(f"   Result: {json.dumps(result, indent=2)}")
            
            # Add function result to messages
            messages.append(
                {
                    "role": "tool",
                    "tool_call_id": tool_call.id,
                    "name": func_name,
                    "content": json.dumps(result),
                }
            )
    
    return "Max iterations reached. Please try again with a simpler request."


# ============================================================
# CLI
# ============================================================


def main():
    import argparse
    
    parser = argparse.ArgumentParser(description="Day 3: Nimbus + LLM Function Calling")
    parser.add_argument(
        "--request",
        default="Send an email to alice@example.com with subject 'Hello' and body 'Test message'",
        help="Natural language request for the agent",
    )
    parser.add_argument(
        "--nimbus-url",
        default="http://localhost:8080",
        help="Nimbus API base URL",
    )
    args = parser.parse_args()
    
    global NIMBUS_BASE_URL
    NIMBUS_BASE_URL = args.nimbus_url
    
    print(f"🚀 Nimbus Integration Demo")
    print(f"   Nimbus URL: {NIMBUS_BASE_URL}")
    print(f"   Request: {args.request}\n")
    
    # Check if Nimbus is running
    try:
        resp = requests.get(f"{NIMBUS_BASE_URL}/health", timeout=2)
        if resp.status_code != 200:
            print("⚠️  Warning: Nimbus health check failed. Is it running?")
            print(f"   Start with: cd ~/workspace/nimbus && go run cmd/gateway/main.go\n")
    except requests.RequestException:
        print("❌ Nimbus is not running!")
        print(f"   Start with: cd ~/workspace/nimbus && go run cmd/gateway/main.go")
        print(f"   Then retry this script.\n")
        return
    
    final_response = run_agent(args.request)
    
    print("\n" + "=" * 60)
    print("✅ Final Response:")
    print(final_response)
    print("=" * 60)


if __name__ == "__main__":
    main()
