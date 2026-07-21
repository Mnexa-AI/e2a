# e2a-sdk-monitor image — continuously exercises the PUBLISHED client
# surfaces (api/python_sdk/mcp/ts_sdk/cli — tests/sdk-monitor/monitor.py)
# against production. Deployed as a Cloud Run service; Cloud Scheduler drives
# POST /tick and e2a delivers to POST /webhook.
#
# Unlike Dockerfile.prober this builds from its own directory as context, not
# the repo root: the image must not be able to see sdks/python, sdks/typescript,
# or cli/, so both the Python and Node dependencies can only come from a real
# `pip install` / `npm install` of the published packages.
FROM python:3.12-slim

# Node is needed for the ts_sdk and cli interfaces (both shell out to a node
# helper / the e2a bin rather than embedding a JS runtime elsewhere). Debian
# bookworm's nodejs/npm packages are recent enough for @e2a/sdk and @e2a/cli
# (both declare "engines": { "node": ">=18" }).
RUN apt-get update \
    && apt-get install -y --no-install-recommends nodejs npm \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Only the pinned published Python SDK — no repo source is copied in.
COPY requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt \
    && pip show e2a

# Only the pinned published npm packages — no repo source is copied in.
# --no-save: package.json already pins exact versions; this just materializes
# node_modules without rewriting it (and without a lockfile to maintain).
COPY package.json /app/package.json
RUN npm install --no-save --omit=dev \
    && npm ls @e2a/sdk @e2a/cli

COPY monitor.py /app/monitor.py
COPY js /app/js

RUN useradd -u 1001 -m e2a
USER e2a

# Cloud Run injects $PORT and monitor.py honors it; 8080 is the local default.
EXPOSE 8080
ENV PYTHONUNBUFFERED=1
ENTRYPOINT ["python", "/app/monitor.py"]
