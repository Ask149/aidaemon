#!/usr/bin/env python3
"""
Google Calendar API utility for aidaemon/Heartbeat v3.

Usage:
  # First-time auth (opens browser for OAuth consent):
  python3 gcal.py auth

  # Create event:
  python3 gcal.py create --title "Meeting" --start "2026-03-04T18:00:00" --end "2026-03-04T19:00:00" --tz "America/Los_Angeles" --desc "Details here" --location "Seattle"

  # Create events from JSON file:
  python3 gcal.py create-batch events.json

  # List upcoming events:
  python3 gcal.py list [--days 7]

Credentials storage:
  ~/.config/aidaemon/gcal-credentials.json   # OAuth client secrets (from Google Cloud Console)
  ~/.config/aidaemon/gcal-token.json         # Refreshable access token (auto-generated)
"""

import argparse
import json
import os
import sys
from datetime import datetime, timedelta
from pathlib import Path

CONFIG_DIR = Path.home() / ".config" / "aidaemon"
CREDENTIALS_FILE = CONFIG_DIR / "gcal-credentials.json"
TOKEN_FILE = CONFIG_DIR / "gcal-token.json"
SCOPES = ["https://www.googleapis.com/auth/calendar"]


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
            print("Refreshing expired token...")
            creds.refresh(Request())
        else:
            if not CREDENTIALS_FILE.exists():
                print(f"ERROR: OAuth credentials not found at {CREDENTIALS_FILE}")
                print()
                print("Setup instructions:")
                print("  1. Go to https://console.cloud.google.com/apis/credentials")
                print("  2. Enable 'Google Calendar API' in APIs & Services > Library")
                print("  3. Create OAuth 2.0 Client ID (Desktop app type)")
                print("  4. Download JSON and save to:")
                print(f"     {CREDENTIALS_FILE}")
                print("  5. Run: python3 gcal.py auth")
                sys.exit(1)

            flow = InstalledAppFlow.from_client_secrets_file(
                str(CREDENTIALS_FILE), SCOPES
            )
            creds = flow.run_local_server(port=0)

        # Save token for future use
        TOKEN_FILE.parent.mkdir(parents=True, exist_ok=True)
        TOKEN_FILE.write_text(creds.to_json())
        os.chmod(TOKEN_FILE, 0o600)
        print(f"Token saved to {TOKEN_FILE}")

    return creds


def get_service():
    """Build Google Calendar API service."""
    from googleapiclient.discovery import build

    creds = get_credentials()
    return build("calendar", "v3", credentials=creds)


def cmd_auth(_args):
    """Authenticate and save token."""
    print("Starting OAuth flow...")
    get_credentials()
    print("Authentication successful!")

    # Quick test: list calendars
    service = get_service()
    calendars = service.calendarList().list().execute()
    print(f"\nAccessible calendars ({len(calendars.get('items', []))}):")
    for cal in calendars.get("items", []):
        primary = " (PRIMARY)" if cal.get("primary") else ""
        print(f"  - {cal['summary']}{primary}")


def cmd_create(args):
    """Create a single calendar event."""
    service = get_service()

    event_body = {
        "summary": args.title,
        "start": {"dateTime": args.start, "timeZone": args.tz},
        "end": {"dateTime": args.end, "timeZone": args.tz},
    }

    if args.desc:
        event_body["description"] = args.desc
    if args.location:
        event_body["location"] = args.location
    if args.color:
        event_body["colorId"] = args.color

    event = service.events().insert(calendarId="primary", body=event_body).execute()
    print(f"✅ Created: {event['summary']}")
    print(f"   Link: {event.get('htmlLink')}")
    return event


def cmd_create_batch(args):
    """Create multiple events from a JSON file."""
    events_file = Path(args.file)
    if not events_file.exists():
        print(f"ERROR: File not found: {events_file}")
        sys.exit(1)

    events = json.loads(events_file.read_text())
    service = get_service()

    created = 0
    for evt in events:
        event_body = {
            "summary": evt["summary"],
            "start": {
                "dateTime": evt["start"],
                "timeZone": evt.get("timeZone", "America/Los_Angeles"),
            },
            "end": {
                "dateTime": evt["end"],
                "timeZone": evt.get("timeZone", "America/Los_Angeles"),
            },
        }

        if evt.get("description"):
            event_body["description"] = evt["description"]
        if evt.get("location"):
            event_body["location"] = evt["location"]
        if evt.get("colorId"):
            event_body["colorId"] = evt["colorId"]

        event = (
            service.events().insert(calendarId="primary", body=event_body).execute()
        )
        print(f"✅ Created: {event['summary']}")
        print(f"   Link: {event.get('htmlLink')}")
        created += 1

    print(f"\n{created}/{len(events)} events created successfully.")


def cmd_list(args):
    """List upcoming events."""
    service = get_service()
    now = datetime.utcnow().isoformat() + "Z"
    end = (datetime.utcnow() + timedelta(days=args.days)).isoformat() + "Z"

    events_result = (
        service.events()
        .list(
            calendarId="primary",
            timeMin=now,
            timeMax=end,
            maxResults=50,
            singleEvents=True,
            orderBy="startTime",
        )
        .execute()
    )

    events = events_result.get("items", [])
    if not events:
        print(f"No events in the next {args.days} days.")
        return

    print(f"Upcoming events (next {args.days} days):")
    for event in events:
        start = event["start"].get("dateTime", event["start"].get("date"))
        print(f"  {start}  {event['summary']}")


def main():
    parser = argparse.ArgumentParser(
        description="Google Calendar API utility for aidaemon"
    )
    sub = parser.add_subparsers(dest="command", help="Command to run")

    # auth
    sub.add_parser("auth", help="Authenticate with Google Calendar API")

    # create
    p_create = sub.add_parser("create", help="Create a single event")
    p_create.add_argument("--title", required=True, help="Event title")
    p_create.add_argument(
        "--start", required=True, help="Start time (ISO 8601, e.g. 2026-03-04T18:00:00)"
    )
    p_create.add_argument(
        "--end", required=True, help="End time (ISO 8601, e.g. 2026-03-04T19:00:00)"
    )
    p_create.add_argument(
        "--tz", default="America/Los_Angeles", help="Timezone (default: America/Los_Angeles)"
    )
    p_create.add_argument("--desc", help="Event description")
    p_create.add_argument("--location", help="Event location")
    p_create.add_argument("--color", help="Color ID (1-11)")

    # create-batch
    p_batch = sub.add_parser("create-batch", help="Create events from JSON file")
    p_batch.add_argument("file", help="JSON file with event definitions")

    # list
    p_list = sub.add_parser("list", help="List upcoming events")
    p_list.add_argument("--days", type=int, default=7, help="Days to look ahead (default: 7)")

    args = parser.parse_args()

    if args.command == "auth":
        cmd_auth(args)
    elif args.command == "create":
        cmd_create(args)
    elif args.command == "create-batch":
        cmd_create_batch(args)
    elif args.command == "list":
        cmd_list(args)
    else:
        parser.print_help()


if __name__ == "__main__":
    main()
