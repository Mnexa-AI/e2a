# e2a-sdk-monitor image — continuously exercises the PUBLISHED Python SDK
# against production (tests/sdk-monitor/monitor.py). Deployed as a Cloud Run
# service; Cloud Scheduler drives POST /tick and e2a delivers to POST /webhook.
#
# Unlike Dockerfile.prober this builds from its own directory as context, not
# the repo root: the image must not be able to see sdks/python, so the only way
# to get the SDK is a real `pip install e2a==<version>` from PyPI.
FROM python:3.12-slim

# Only the pinned published SDK — no repo source is copied in.
COPY requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt

COPY monitor.py /app/monitor.py
WORKDIR /app

RUN useradd -u 1001 -m e2a
USER e2a

# Cloud Run injects $PORT and monitor.py honors it; 8080 is the local default.
EXPOSE 8080
ENV PYTHONUNBUFFERED=1
ENTRYPOINT ["python", "/app/monitor.py"]
