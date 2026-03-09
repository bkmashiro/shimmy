FROM python:3.11-slim

# 创建非root用户
RUN useradd -u 1000 -m lambdauser

# 复制脚本
COPY lambda_probe.py /app/
WORKDIR /app

# 切换用户
USER lambdauser

CMD ["python3", "lambda_probe.py"]
