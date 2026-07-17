# 本地域名路由使用自定义完整域名与本地 CA

Web Route 允许用户使用任意完整域名，而非限制为 `*.test`。这是为了让本地入口可以使用远端 HTTPS 服务证书中的真实域名；当该域名经受托管 hosts 映射到回环地址时，浏览器访问本地 Caddy，而 Caddy 再通过 SSH Forward 访问远端服务。

所有本地入口都强制使用 Caddy 的本地 CA（`tls internal`），不得尝试通过 ACME 申请公网证书。对 HTTPS 上游，用户必须明确配置 TLS SNI 名称，Caddy 按该名称校验远端证书并设置相应的上游 Host 请求头。这样既支持远端 HTTPS 的正确校验，也避免对本地 hosts 覆盖域名产生意外的公网证书申请。
