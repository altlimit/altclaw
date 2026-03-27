FROM node:22-slim

# Install Python 3 and common build tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 python3-pip python3-venv curl ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

# Install uv (fast Python package manager)
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.local/bin:$PATH"

# Global npm packages go to a separate prefix so they can be volume-mounted
ENV NPM_CONFIG_PREFIX=/opt/npm-global
ENV PATH="/opt/npm-global/bin:$PATH"
RUN mkdir -p /opt/npm-global

# Verify installations
RUN node --version && npm --version && python3 --version && uv --version

WORKDIR /workspace
