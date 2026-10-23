FROM python:3.12-slim

# 国内镜像源加速
RUN set -eux; \
    sed -i 's|deb.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        git \
        jq \
        ripgrep \
        nodejs \
        npm \
    ; \
    rm -rf /var/lib/apt/lists/*

# 常用 pip 包最小集
RUN pip install --no-cache-dir -i https://mirrors.aliyun.com/pypi/simple/ \
        requests \
        httpx \
        pydantic \
    && rm -rf /root/.cache

WORKDIR /

CMD ["bash"]
