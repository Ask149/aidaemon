#!/usr/bin/env python3
"""
Gmail API utility for aidaemon/Heartbeat v3.

Usage:
  # First-time auth (opens browser for OAuth consent):
  python3.11 gmail.py auth

  # List recent emails (default: 10):
  python3.11 gmail.py list [--max 20]

  # List unread emails only:
  python3.11 gmail.py unread [--max 10]

  # Read a specific email by ID:
  python3.11 gmail.py read <message-id>

  # Search emails:
  python3.11 gmail.py search "from:someone@example.com" [--max 10]

  # Summary for awareness agent (unread count + important senders):
  python3.11 gmail.py summary

Credentials storage:
  ~/.config/aidaemon/gcal-credentials.json   # Same OAuth client secrets (shared with gcal.py)
  ~/.config/aidaemon/gmail-token.json        # Separate token (different scopes + account)
"""

import argparse
import base64
import json
import os
import re
import sys
from datetime import datetime
from email.utils import parsedate_to_datetime
from html import unescape
from pathlib import Path

CONFIG_DIR = Path.home() / ".config" / "aidaemon"
CREDENTIALS_FILE = CONFIG_DIR / "gcal-credentials.json"  # Shared OAuth client
TOKEN_FILE = CONFIG_DIR / "gmail-token.json"  # Separate token for Gmail
SCOPES = ["https://www.googleapis.com/auth/gmail.readonly"]


def get_credentials():
    """Load or refresh OAuth credentials."""
    from google.auth.transport.requests import Request
    from google.oauth2.credentials import Credentials
    from google_auth_oauthlib.flow import InstalledAppFlow

    creds = None

    # Load existing token
    if TOKEN_FILE.exists():
        creds = Credentials.from_authorized_user_file(str(TOKEN_FILE), SCOPES)

    # Refresh or re-auth
    if not creds or not creds.valid:
        if creds and creds.expired and creds.refresh_token:
            print("Refreshing expired token...", file=sys.stderr)
            creds.refresh(Request())
        else:
            if not CREDENTIALS_FILE.exists():
                print(f"ERROR: OAuth credentials not found at {CREDENTIALS_FILE}")
                print()
                print("Setup instructions:")
                print("  1. Go to https://console.cloud.google.com/apis/credentials")
                print("  2. Enable 'Gmail API' in APIs & Services > Library")
                print("  3. Use existing OAuth 2.0 Client ID (Desktop app 'aidaemon')")
                print("  4. Ensure credentials JSON exists at:")
                print(f"     {CREDENTIALS_FILE}")
                print("  5. Run: python3.11 gmail.py auth")
                sys.exit(1)

            flow = InstalledAppFlow.from_client_secrets_file(
                str(CREDENTIALS_FILE), SCOPES
            )
            creds = flow.run_local_server(port=0)

        # Save token for future use
        TOKEN_FILE.parent.mkdir(parents=True, exist_ok=True)
        TOKEN_FILE.write_text(creds.to_json())
        os.chmod(TOKEN_FILE, 0o600)
        print(f"Token saved to {TOKEN_FILE}", file=sys.stderr)

    return creds


def get_service():
    """Build Gmail API service."""
    from googleapiclient.discovery import build

    creds = get_credentials()
    return build("gmail", "v1", credentials=creds)


def get_header(headers, name):
    """Extract a header value from message headers list."""
    for h in headers:
        if h["name"].lower() == name.lower():
            return h["value"]
    return ""


def strip_html(text):
    """Basic HTML tag stripping."""
    text = re.sub(r"<br\s*/?>", "\n", text, flags=re.IGNORECASE)
    text = re.sub(r"<[^>]+>", "", text)
    return unescape(text).strip()


def get_body_text(payload):
    """Extract plain text body from message payload."""
    # Simple message with body
    if payload.get("body", {}).get("data"):
        mime = payload.get("mimeType", "")
        data = base64.urlsafe_b64decode(payload["body"]["data"]).decode("utf-8", errors="replace")
        if "html" in mime:
            return strip_html(data)
        return data

    # Multipart — prefer text/plain, fall back to text/html
    parts = payload.get("parts", [])
    plain = ""
    html = ""
    for part in parts:
        mime = part.get("mimeType", "")
        if part.get("body", {}).get("data"):
            decoded = base64.urlsafe_b64decode(part["body"]["data"]).decode("utf-8", errors="replace")
            if mime == "text/plain":
                plain = decoded
            elif mime == "text/html":
                html = decoded
        # Nested multipart
        elif part.get("parts"):
            nested = get_body_text(part)
            if nested:
                plain = plain or nested

    if plain:
        return plain
    if html:
        return strip_html(html)
    return "(no text body)"


def format_email_line(msg):
    """Format a single email as a one-line summary."""
    headers = msg.get("payload", {}).get("headers", [])
    subject = get_header(headers, "Subject") or "(no subject)"
    sender = get_header(headers, "From")
    date_str = get_header(headers, "Date")
    unread = "UNREAD" in msg.get("labelIds", [])

    # Parse sender to short form
    match = re.match(r'"?([^"<]+)"?\s*<', sender)
    sender_short = match.group(1).strip() if match else sender.split("@")[0]

    # Parse date
    try:
        dt = parsedate_to_datetime(date_str)
        date_fmt = dt.strftime("%b %d %H:%M")
    except Exception:
        date_fmt = date_str[:16] if date_str else ""

    flag = "📩" if unread else "  "
    return f"{flag} {date_fmt}  {sender_short:20s}  {subject}"


def cmd_auth(_args):
    """Authenticate and save token."""
    print("Starting OAuth flow...")
    print("NOTE: Sign in with the Gmail account you want to monitor")
    get_credentials()
    print("Authentication successful!")

    # Quick test: get profile
    service = get_service()
    profile = service.users().getProfile(userId="me").execute()
    print(f"\nAuthenticated as: {profile['emailAddress']}")
    print(f"Total messages: {profile.get('messagesTotal', 'N/A')}")
    print(f"Total threads: {profile.get('threadsTotal', 'N/A')}")


def cmd_list(args):
    """List recent emails."""
    service = get_service()
    results = service.users().messages().list(
        userId="me", maxResults=args.max
    ).execute()

    messages = results.get("messages", [])
    if not messages:
        print("No messages found.")
        return

    print(f"Recent emails ({len(messages)}):")
    for msg_ref in messages:
        msg = service.users().messages().get(
            userId="me", id=msg_ref["id"], format="metadata",
            metadataHeaders=["Subject", "From", "Date"]
        ).execute()
        print(format_email_line(msg))


def cmd_unread(args):
    """List unread emails."""
    service = get_service()
    results = service.users().messages().list(
        userId="me", q="is:unread", maxResults=args.max
    ).execute()

    messages = results.get("messages", [])
    if not messages:
        print("No unread messages.")
        return

    print(f"Unread emails ({len(messages)}):")
    for msg_ref in messages:
        msg = service.users().messages().get(
            userId="me", id=msg_ref["id"], format="metadata",
            metadataHeaders=["Subject", "From", "Date"]
        ).execute()
        print(format_email_line(msg))


def cmd_read(args):
    """Read a specific email by ID."""
    service = get_service()
    msg = service.users().messages().get(
        userId="me", id=args.message_id, format="full"
    ).execute()

    headers = msg.get("payload", {}).get("headers", [])
    print(f"From:    {get_header(headers, 'From')}")
    print(f"To:      {get_header(headers, 'To')}")
    print(f"Date:    {get_header(headers, 'Date')}")
    print(f"Subject: {get_header(headers, 'Subject')}")
    print(f"Labels:  {', '.join(msg.get('labelIds', []))}")
    print("─" * 60)

    body = get_body_text(msg.get("payload", {}))
    # Truncate very long bodies
    if len(body) > 3000:
        body = body[:3000] + "\n\n... (truncated, full message is longer)"
    print(body)


def cmd_search(args):
    """Search emails with Gmail query syntax."""
    service = get_service()
    results = service.users().messages().list(
        userId="me", q=args.query, maxResults=args.max
    ).execute()

    messages = results.get("messages", [])
    if not messages:
        print(f"No messages matching: {args.query}")
        return

    print(f"Search results for '{args.query}' ({len(messages)}):")
    for msg_ref in messages:
        msg = service.users().messages().get(
            userId="me", id=msg_ref["id"], format="metadata",
            metadataHeaders=["Subject", "From", "Date"]
        ).execute()
        print(format_email_line(msg))


def cmd_summary(_args):
    """Quick summary for awareness agent — unread count, labels, recent important."""
    service = get_service()

    # Get profile
    profile = service.users().getProfile(userId="me").execute()

    # Count unread
    unread_results = service.users().messages().list(
        userId="me", q="is:unread", maxResults=100
    ).execute()
    unread_count = unread_results.get("resultSizeEstimate", 0)

    # Get unread in INBOX specifically
    inbox_unread = service.users().messages().list(
        userId="me", q="is:unread in:inbox", maxResults=50
    ).execute()
    inbox_unread_count = inbox_unread.get("resultSizeEstimate", 0)

    # Get unread messages for sender breakdown
    unread_msgs = inbox_unread.get("messages", [])

    print(f"Email: {profile['emailAddress']}")
    print(f"Inbox unread: {inbox_unread_count}")
    print(f"Total unread: {unread_count}")

    if unread_msgs:
        print(f"\nRecent unread ({min(len(unread_msgs), 10)}):")
        for msg_ref in unread_msgs[:10]:
            msg = service.users().messages().get(
                userId="me", id=msg_ref["id"], format="metadata",
                metadataHeaders=["Subject", "From", "Date"]
            ).execute()
            print(format_email_line(msg))


def main():
    parser = argparse.ArgumentParser(
        description="Gmail API utility for aidaemon"
    )
    sub = parser.add_subparsers(dest="command", help="Command to run")

    # auth
    sub.add_parser("auth", help="Authenticate with Gmail API")

    # list
    p_list = sub.add_parser("list", help="List recent emails")
    p_list.add_argument("--max", type=int, default=10, help="Max emails (default: 10)")

    # unread
    p_unread = sub.add_parser("unread", help="List unread emails")
    p_unread.add_argument("--max", type=int, default=10, help="Max emails (default: 10)")

    # read
    p_read = sub.add_parser("read", help="Read a specific email")
    p_read.add_argument("message_id", help="Message ID to read")

    # search
    p_search = sub.add_parser("search", help="Search emails")
    p_search.add_argument("query", help="Gmail search query (e.g. 'from:boss@company.com')")
    p_search.add_argument("--max", type=int, default=10, help="Max results (default: 10)")

    # summary
    sub.add_parser("summary", help="Quick summary for awareness agent")

    args = parser.parse_args()

    commands = {
        "auth": cmd_auth,
        "list": cmd_list,
        "unread": cmd_unread,
        "read": cmd_read,
        "search": cmd_search,
        "summary": cmd_summary,
    }

    if args.command in commands:
        commands[args.command](args)
    else:
        parser.print_help()


if __name__ == "__main__":
    main()
