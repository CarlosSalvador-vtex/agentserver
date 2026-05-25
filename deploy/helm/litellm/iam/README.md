# IAM — LiteLLM Bedrock IRSA

Role: `dev-ti-eks-analytics-platform-litellm-bedrock-role`
Cluster: `dev-ti-eks-analytics-platform` (us-east-1)
Namespace/SA: `agentserver/litellm`

## Create

```bash
# 1. Role com trust policy IRSA
aws iam create-role \
  --role-name dev-ti-eks-analytics-platform-litellm-bedrock-role \
  --assume-role-policy-document file://trust-policy.json \
  --region us-east-1

# 2. Inline policy com permissões Bedrock
aws iam put-role-policy \
  --role-name dev-ti-eks-analytics-platform-litellm-bedrock-role \
  --policy-name bedrock-invoke-claude \
  --policy-document file://policy.json
```

## Models cobertos

| Model | ARN |
|---|---|
| claude-3-sonnet | `arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-3-sonnet-20240229-v1:0` |
| claude-3-haiku | `arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-3-haiku-20240307-v1:0` |
| claude-sonnet-4-6 (us-east-1) | `arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-sonnet-4-6` |
| claude-sonnet-4-6 (us-east-2) | `arn:aws:bedrock:us-east-2::foundation-model/anthropic.claude-sonnet-4-6` |
| claude-sonnet-4-6 (us-west-2) | `arn:aws:bedrock:us-west-2::foundation-model/anthropic.claude-sonnet-4-6` |
| Inference Profile | `arn:aws:bedrock:us-east-1:344729309528:application-inference-profile/62r7btpf0s40` |

Todas as 3 regions são necessárias porque o inference profile `OpenClaw-Claude-Sonnet-4-6`
distribui tráfego entre us-east-1, us-east-2 e us-west-2.
