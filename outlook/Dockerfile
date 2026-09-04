FROM ghcr.io/astral-sh/uv:python3.13-bookworm-slim

# Prevents Python from writing .pyc files and buffers stdout/stderr
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app/outlook

# Install system deps (if any needed) - using apt for Debian
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    git \
    && rm -rf /var/lib/apt/lists/*

COPY outlook/pyproject.toml outlook/uv.lock ./
COPY metrics-python /app/metrics-python

# Install dependencies only (without building the local package)
RUN uv sync --frozen --no-dev --no-install-project

COPY outlook/app/ ./app/

RUN uv sync --frozen --no-dev --no-editable

EXPOSE 9000

# Start the server using uv run (ensures proper virtual environment)
CMD ["uv", "run", "python", "-m", "app.main"]
