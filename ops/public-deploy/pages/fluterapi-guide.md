# FluterAPI 充值与教程

欢迎使用 FluterAPI。第一次使用时，按下面四步走就可以：

1. 购买额度卡密：<a href="https://catfk.com/shop/VYYIQSK9" target="_blank" rel="noopener noreferrer">卡密购买小店（无手续费）</a>；原 <a href="https://pay.ldxp.cn/shop/IMZJ600N" target="_blank" rel="noopener noreferrer">支付小店（有手续费）</a> 仍可作为备用入口
2. 回站内兑换额度或并发：[兑换码页面](/redeem)
3. 创建自己的 API Key：[API 密钥页面](/keys)
4. 按教程把 API Key 和 Base URL 填到 Codex Desktop、CCSwitch 或其他客户端

## 合规公告 / Compliance Notice

因政策与合规要求，即日起本站不再向中国大陆地区及其他不符合条件地区提供 AI 中转服务。

Due to policy and compliance requirements, effective immediately, this service no longer provides AI relay services to IP addresses from mainland China or other ineligible regions.

支持地区用户不受影响；受影响地区访问会看到提示页面。若有余额退款事项，请联系管理员处理。

Users in supported regions are unaffected. Affected visitors will see a notice page. For remaining balance or refund matters, please contact the administrator.

## 新手完整教程

完整图文教程已经整理到独立页面，点击打开：

[https://fluterapi.top/docs/](https://fluterapi.top/docs/)

教程里包括：

- 怎么购买卡密，兑换余额或并发
- 怎么创建 API Key
- Codex Desktop 和 CCSwitch 怎么配置
- Base URL 应该填什么
- 模型怎么选
- 生图怎么用
- 401、429、502、503 等常见报错怎么判断

## 技术经验分享群

QQ群：**229863202**

新用户配置 Codex Desktop、CCSwitch、生图、模型选择或遇到报错时，可以进群交流。提问时最好带上：

- 使用的模型名
- 请求路径，例如 `/v1/responses`、`/v1/chat/completions`、`/v1/images/generations`
- 报错截图或完整错误文字
- 大概出错时间

请不要在群里发送自己的 API Key。API Key 等于你的接口密码，别人拿到后可能会消耗你的余额。

## 常用入口

| 用途 | 地址 |
| --- | --- |
| 首页总入口 | [https://fluterapi.top](https://fluterapi.top) |
| 完整教程 | [https://fluterapi.top/docs/](https://fluterapi.top/docs/) |
| API 控制台 | [https://api.fluterapi.top](https://api.fluterapi.top) |
| 安装 Codex Desktop | [OpenAI 官方 Codex 页面](https://chatgpt.com/zh-Hans-CN/codex/) |
| 卡密购买小店（无手续费） | <a href="https://catfk.com/shop/VYYIQSK9" target="_blank" rel="noopener noreferrer">https://catfk.com/shop/VYYIQSK9</a> |
| 原支付小店（有手续费） | <a href="https://pay.ldxp.cn/shop/IMZJ600N" target="_blank" rel="noopener noreferrer">https://pay.ldxp.cn/shop/IMZJ600N</a> |
| 兑换额度/并发 | [兑换码页面](/redeem) |
| 创建 API Key | [API 密钥页面](/keys) |
| 查看模型/通道 | [可用渠道页面](/available-channels) |

## 客户端核心配置

无论你用 Codex Desktop、CCSwitch、Cursor、Cherry Studio，核心都是这几项：

| 项目 | 填写内容 |
| --- | --- |
| Base URL / API Host | `https://api.fluterapi.top/v1` |
| API Key / Token | 你在 [API 密钥页面](/keys) 创建的 Key |
| Authorization | `Bearer 你的 API Key` |
| 图片模型 | `gpt-image-2` |

注意：`/v1` 必须小写，不要写成 `/V1`。

## 兑换页面怎么看

打开 [兑换码页面](/redeem) 后，顶部会显示当前余额和并发数；中间输入兑换码；下方最近活动会显示余额充值、并发增加等记录。

兑换成功后请刷新页面，确认余额或并发数是否增加。兑换码区分大小写，请完整复制，不要多空格。

## 快速排错

### 401 Unauthorized / INVALID_API_KEY

通常是 API Key 填错、复制不完整、Key 被删除，或者把登录密码当成 API Key 了。回到 [API 密钥页面](/keys) 重新复制。

### 429 Too Many Requests

通常是请求太频繁、并发太高，或者上游临时限流。先等一会儿再试，必要时降低并发或换模型。

### 502 / 503

通常是上游服务临时不可用、账号池异常或请求时间较长。先等 1 分钟重试；如果持续出现，带上模型名、报错和时间进群。

### Image generation is not enabled for this group

当前 API Key 所在分组没有开启生图。请换生图分组的 Key，或联系管理员调整。

## 模型怎么选

| 场景 | 推荐 |
| --- | --- |
| 便宜、快速、小任务 | `gpt-5.4-mini` |
| 日常主力、写作、代码 | `gpt-5.4`、`gpt-5.5` |
| Codex 代码任务 | `gpt-5.3-codex`、`gpt-5.3-codex-spark` |
| Claude 写作/长文本/代码 | `claude-sonnet-4-6`、`claude-opus-4-7` |
| DeepSeek 高性价比 | `deepseek-v4-flash`、`deepseek-v4-pro` |
| 生图 | `gpt-image-2` |

完整实时列表请看 [可用渠道页面](/available-channels)。
