"""Local end-to-end test. Run the frontend dev server first:

    cd frontend && npm run dev

Then run the signaling server:

    # from ../fernsicht-server
    .venv/bin/python signaling.py

Then run this script:

    cd publishers/python && .venv/bin/python ../../test_local.py
"""

import time

from fernsicht import blick

LOCAL_URL = "http://localhost:5173/fernsicht/"
LOCAL_SIGNALING_URL = "ws://localhost:8080/ws"

for i in blick(
    range(50),
    desc="Local test",
    base_url=LOCAL_URL,
    signaling_url=LOCAL_SIGNALING_URL,
):
    time.sleep(0.2)
