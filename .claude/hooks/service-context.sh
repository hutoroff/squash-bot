#!/usr/bin/env python3
"""
UserPromptSubmit hook: injects service architecture context when the user's prompt
indicates they are planning changes to a specific service.

Detects service by scanning for path patterns or unique type/file names.
Only fires when the prompt also contains a planning/change keyword to avoid
injecting context on every casual mention.
"""
import json
import sys
import os

data = json.load(sys.stdin)
prompt = data.get("prompt", "").lower()

# Only inject context when the prompt looks like a planning or implementation request.
CHANGE_KEYWORDS = {
    "add", "implement", "change", "fix", "update", "refactor", "create", "modify",
    "build", "write", "plan", "planning", "design", "migrate", "extend", "remove",
    "delete", "new feature", "how to", "how do", "should i", "need to", "want to",
    "let's", "lets ", "can you", "could you", "please", "i want", "i need",
}
is_planning = any(kw in prompt for kw in CHANGE_KEYWORDS)
if not is_planning:
    sys.exit(0)

# Service detection: specific patterns unique to each service.
DETECTORS = {
    "management": [
        "cmd/management", "management service", "management api",
        "gameservice", "participationservice", "venueservice",
        "cancellationreminderjob", "bookingremindjob", "autobookingjob", "dayaftercleanup",
        "gamenotifier", "gamerepository", "participationrepository",
        "venuerepo", "grouprepo", "playerrepo", "guestrepository",
        "scheduler.go", "booking_reminder", "cancellation_reminder",
        "auto_booking.go", "day_after_cleanup",
    ],
    "telegram": [
        "cmd/telegram", "telegram bot", "telegram service",
        "callbackrouter", "callback_router",
        "newgamewizard", "newgame_handlers",
        "venuewizard", "venue_handlers",
        "game_manage_handlers", "participation_handlers",
        "settings_handlers", "pendingcourtsed",
        "managementclient", "bot struct", "handlemessage",
        "handlecallback", "handlecommand",
    ],
    "booking": [
        "cmd/booking", "booking service", "eversports",
        "createbooking", "getslots", "cancelmatch", "getfacility", "getcourts",
        "withauth", "bookingmu", "checkout.go", "matches.go", "slots.go",
        "facility.go", "eversportsclient",
    ],
    "web": [
        "cmd/web", "web service", "web ui", "webserver",
        "authhandler", "gameshandler", "handlecallback",
        "telegram login widget", "jwt", "jwt_secret",
        "spa", "react frontend", "vite", "web/frontend",
        "handlelistgames", "handlegetparticipants",
    ],
}

SKILL_DIR = os.path.join(os.path.dirname(__file__), "..", "skills")

detected = []
for service, keywords in DETECTORS.items():
    if any(kw in prompt for kw in keywords):
        detected.append(service)

if not detected:
    sys.exit(0)

# Read skill content for each detected service.
parts = []
for service in detected:
    skill_path = os.path.join(SKILL_DIR, service, "SKILL.md")
    try:
        with open(skill_path) as f:
            content = f.read()
        # Strip YAML frontmatter (between first two '---' lines).
        if content.startswith("---"):
            end = content.index("---", 3)
            content = content[end + 3:].lstrip()
        parts.append(content)
    except (OSError, ValueError):
        parts.append(f"[{service} skill file not found at {skill_path}]")

context = "\n\n---\n\n".join(parts)
preamble = (
    f"[AUTO-INJECTED: Architecture context for {', '.join(detected)} service(s). "
    "Review before planning changes.]\n\n"
)

output = {
    "hookSpecificOutput": {
        "hookEventName": "UserPromptSubmit",
        "additionalContext": preamble + context,
    }
}
print(json.dumps(output))
