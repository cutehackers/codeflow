#!/usr/bin/env python3
import json
import sys
import re

try:
    data = json.load(sys.stdin)
    cmd = data.get("toolCall", {}).get("args", {}).get("CommandLine", "")
    
    # Block git commit, tag, push commands
    if re.search(r'\bgit\s+(commit|tag|push)\b', cmd):
        print(json.dumps({
            "decision": "deny",
            "reason": "Execution blocked: Antigravity is restricted from committing, tagging, or pushing code. Please review the changes and commit manually."
        }))
        sys.exit(0)
except Exception:
    pass

print(json.dumps({"decision": "allow"}))
