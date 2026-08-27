# Bashupload Helm Chart

用于在 Kubernetes 上部署 bashupload 文件上传服务的 Helm Chart。

## 特性

- **DaemonSet 部署**：在每个工作节点上运行 bashupload 实例
- **Nginx Sidecar**：TLS 终止和反向代理（hostPort 80/443）
- **cert-manager 集成**：自动从 Let's Encrypt 获取通配符证书
- **Cloudflare DNS-01**：支持 DNS-01 验证方式

## 快速开始

### 添加 Helm 仓库

```bash
helm repo add bashupload https://dulljz.github.io/bashupload-r2
helm repo update
```

### 安装

#### 方式一：使用配置文件

```bash
# 创建 values 配置文件
cat > my-values.yaml <<EOF
r2:
  accountId: "your-account-id"
  accessKeyId: "your-access-key"
  secretAccessKey: "your-secret-key"
  bucketName: "bashupload"

dedup:
  # 使用至少 32 字节的随机值，并将 my-values.yaml 作为私密文件保存
  secret: "your-random-dedup-secret"

tls:
  clusterIssuer:
    email: "your-email@example.com"
    cloudflare:
      apiToken: "your-cloudflare-api-token"
EOF

# 安装
helm install bashupload bashupload/bashupload -f my-values.yaml
```

#### 方式二：使用 --set 参数

不创建配置文件，直接通过命令行参数安装：

```bash
helm install bashupload bashupload/bashupload \
  --set r2.accountId="your-account-id" \
  --set r2.accessKeyId="your-access-key" \
  --set r2.secretAccessKey="your-secret-key" \
  --set r2.bucketName="bashupload" \
  --set-string dedup.secret="your-random-dedup-secret" \
  --set tls.clusterIssuer.email="your-email@example.com" \
  --set tls.clusterIssuer.cloudflare.apiToken="your-cloudflare-api-token"
```

### 前置要求

安装 cert-manager：

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.0/cert-manager.yaml
```

## 配置项

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `r2.accountId` | Cloudflare R2 账户 ID | `""` |
| `r2.accessKeyId` | R2 Access Key ID | `""` |
| `r2.secretAccessKey` | R2 Secret Access Key | `""` |
| `r2.bucketName` | R2 存储桶名称 | `"bashupload"` |
| `dedup.secret` | 内容去重 HMAC 私钥（启用去重时必需，存入 Kubernetes Secret） | `""` |
| `config.maxUploadSize` | 最大上传大小（字节） | `"5368709120"` |
| `config.maxAge` | 文件最大保存时间（秒） | `"3600"` |
| `config.enableDedup` | 是否启用带有效期上传的内容去重 | `"true"` |
| `config.uploadRateLimit` | 单 IP 每窗口最大上传次数（`"0"` 禁用） | `"10"` |
| `config.uploadRateLimitWindow` | 上传限流窗口时长（秒） | `"60"` |
| `config.trustProxyHeaders` | 是否信任代理头识别客户端 IP（nginx sidecar 场景保持 `"true"`） | `"true"` |
| `tls.clusterIssuer.email` | Let's Encrypt 邮箱 | `"example@example.com"` |
| `nodeAffinity.excludeNodes` | 排除的节点列表 | `[]` |

完整配置请参考 [values.yaml](./values.yaml)。

### 节点调度

默认情况下，DaemonSet 会在**所有节点**上部署 Pod（包括控制平面节点）。

如果需要排除特定节点（如控制平面节点），可以配置 `nodeAffinity.excludeNodes`：

```bash
# 排除单个节点
helm install bashupload bashupload/bashupload \
  --set nodeAffinity.excludeNodes[0]="control-plane-node"

# 排除多个节点
helm install bashupload bashupload/bashupload \
  --set nodeAffinity.excludeNodes[0]="node1" \
  --set nodeAffinity.excludeNodes[1]="node2"
```

或在 values 文件中配置：

```yaml
nodeAffinity:
  excludeNodes:
    - control-plane-node
    - another-node
```


## 架构

详细请见博客 https://dulljzblog.jz-home.top/2025/12/20/bashupload-dot-app-deployment-in-k8s/

```
┌─────────────────────────────────────────────────────────┐
│                    Public DNS (多 A 记录)                │
└─────────────────────────┬───────────────────────────────┘
                          │
          ┌───────────────┼───────────────┐
          ▼               ▼               ▼
     ┌─────────┐     ┌─────────┐     ┌─────────┐
     │ Worker1 │     │ Worker2 │     │ Worker3 │
     │ ┌─────┐ │     │ ┌─────┐ │     │ ┌─────┐ │
     │ │Nginx│ │     │ │Nginx│ │     │ │Nginx│ │
     │ │:443 │ │     │ │:443 │ │     │ │:443 │ │
     │ └──┬──┘ │     │ └──┬──┘ │     │ └──┬──┘ │
     │    ▼    │     │    ▼    │     │    ▼    │
     │ ┌─────┐ │     │ ┌─────┐ │     │ ┌─────┐ │
     │ │ App │ │     │ │ App │ │     │ │ App │ │
     │ │:3000│ │     │ │:3000│ │     │ │:3000│ │
     │ └─────┘ │     │ └─────┘ │     │ └─────┘ │
     └─────────┘     └─────────┘     └─────────┘
```

## 许可证

MIT
