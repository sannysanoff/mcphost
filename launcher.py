import argparse
import requests
import json

BASE_URL = "http://localhost:9262"

def pretty_print_json(data):
    """Prints JSON data in a readable format."""
    print(json.dumps(data, indent=2, sort_keys=True))

def start_job(model, system_message, user_query):
    """
    Starts a new job on the mcphost server.
    """
    url = f"{BASE_URL}/start"
    payload = {
        "system_message": system_message,
        "user_query": user_query,
    }
    if model: # Only include model_id if provided
        payload["model_id"] = model

    headers = {"Content-Type": "application/json"}
    try:
        response = requests.post(url, json=payload, headers=headers)
        response.raise_for_status()  # Raise an exception for bad status codes
        print("Job Start Response:")
        pretty_print_json(response.json())
    except requests.exceptions.RequestException as e:
        print(f"Error starting job: {e}")
        if hasattr(e, 'response') and e.response is not None:
            try:
                print("Server Response:")
                pretty_print_json(e.response.json())
            except json.JSONDecodeError:
                print(f"Raw Server Response: {e.response.text}")


def cancel_job(job_id):
    """
    Cancels (stops) an existing job on the mcphost server.
    """
    url = f"{BASE_URL}/stop"
    params = {"id": job_id}
    try:
        response = requests.get(url, params=params)
        response.raise_for_status()
        print(f"Job Cancel Response (Job ID: {job_id}):")
        pretty_print_json(response.json())
    except requests.exceptions.RequestException as e:
        print(f"Error cancelling job {job_id}: {e}")
        if hasattr(e, 'response') and e.response is not None:
            try:
                print("Server Response:")
                pretty_print_json(e.response.json())
            except json.JSONDecodeError:
                print(f"Raw Server Response: {e.response.text}")

def get_status():
    """
    Gets the current status of the mcphost server.
    """
    url = f"{BASE_URL}/status"
    try:
        response = requests.get(url)
        response.raise_for_status()
        print("Server Status Response:")
        pretty_print_json(response.json())
    except requests.exceptions.RequestException as e:
        print(f"Error getting status: {e}")
        if hasattr(e, 'response') and e.response is not None:
            try:
                print("Server Response:")
                pretty_print_json(e.response.json())
            except json.JSONDecodeError:
                print(f"Raw Server Response: {e.response.text}")

def get_models():
    """
    Gets the list of available models from the mcphost server.
    """
    url = f"{BASE_URL}/models"
    try:
        response = requests.get(url)
        response.raise_for_status()
        print("Available Models Response:")
        pretty_print_json(response.json())
    except requests.exceptions.RequestException as e:
        print(f"Error getting models: {e}")
        if hasattr(e, 'response') and e.response is not None:
            try:
                print("Server Response:")
                pretty_print_json(e.response.json())
            except json.JSONDecodeError:
                print(f"Raw Server Response: {e.response.text}")

def main():
    parser = argparse.ArgumentParser(description="MCPHost Launcher: Interact with the mcphost server.")
    subparsers = parser.add_subparsers(dest="command", required=True, help="Sub-command help")

    # Start command
    start_parser = subparsers.add_parser("start", help="Start a new job")
    start_parser.add_argument("--model", required=False, help="Model ID to use (e.g., claude-3-5-sonnet-latest). If omitted, server uses its default.")
    start_parser.add_argument("--system-message", default="", help="System message for the AI model (optional)")
    start_parser.add_argument("--user-query", required=True, help="User query for the AI model")

    # Cancel command
    cancel_parser = subparsers.add_parser("cancel", help="Cancel (stop) an existing job")
    cancel_parser.add_argument("--job-id", required=True, help="ID of the job to cancel")

    # Status command
    status_parser = subparsers.add_parser("status", help="Get the current server status")

    # Models command
    models_parser = subparsers.add_parser("models", help="List available models from the server")

    args = parser.parse_args()

    if args.command == "start":
        start_job(args.model, args.system_message, args.user_query)
    elif args.command == "cancel":
        cancel_job(args.job_id)
    elif args.command == "status":
        get_status()
    elif args.command == "models":
        get_models()
    else:
        parser.print_help()

if __name__ == "__main__":
    main()
