# Agent 交互卡片

Agent 通过独立的 `<bridge_card>` 消息请求发送交互卡片。Go bridge 使用当前聊天和发送者信息生成签名，保存待提交请求，并通过已有的 Lark transport 发送 CardKit 2.0 卡片。

```text
<bridge_card>{"id":"application-settings","card":{"schema":"2.0","header":{"title":{"tag":"plain_text","content":"应用设置"},"template":"blue"},"body":{"elements":[{"tag":"form","name":"application_settings","elements":[{"tag":"select_static","name":"environment","required":true,"placeholder":{"tag":"plain_text","content":"请选择环境"},"options":[{"text":{"tag":"plain_text","content":"测试环境"},"value":"test"},{"text":{"tag":"plain_text","content":"生产环境"},"value":"production"}]},{"tag":"input","name":"app_name","required":true,"label":{"tag":"plain_text","content":"应用名称"}},{"tag":"button","name":"submit_application","form_action_type":"submit","type":"primary_filled","text":{"tag":"plain_text","content":"提交"}}]}]}}}</bridge_card>
```

Agent 输出消息时需要去掉示例外面的代码围栏。`id` 在本轮中标识请求；同一请求重复出现时只发送一次。协议消息可以出现在过程回复或最终回复中。

表单中的输入和选择组件通过提交按钮统一回传。普通按钮可以在 `behaviors` 中指定 `callback`，并在 `value` 中填写业务字段。提交按钮省略 `behaviors` 时，bridge 自动添加回调。

`cmd`、`__bridge_cb`、`__claude_cb`、`bridge_token`、`form_value` 为保留字段，agent 提供的按钮业务数据不能包含这些字段。bridge 校验卡片后自动填写签名。同一卡片的全部按钮共用一次提交机会。

发送请求后，agent 结束本轮。用户提交表单后，bridge 将下面的内容交给聊天对应的会话：

```text
[card-click] {"form_value":{"app_name":"example-app","environment":"production"}}
```

bridge 校验聊天、会话范围、提交者、按钮业务字段、签名及有效期。`CallbackTTL` 控制有效期，默认 24 小时。停止执行按钮仍然要求对应执行处于活动状态。

配置 `CallbackAuthOptions.NonceStorePath` 时，待提交请求保存在该路径加 `.pending` 后缀的文件中；默认 CLI 使用当前 profile 的 `callback-nonces.json.pending`。文件权限为 `0600`，内容保存 token 的摘要及验证上下文。bridge 重启后继续读取这些请求，已提交的卡片不能重复提交。SDK 使用内存 nonce store 时，待提交请求也仅保存在内存中。

卡片发送失败时，bridge 取消对应待提交请求，并提示用户通过文字回复。展示卡片可继续使用官方 CLI 的 `im +messages-send --msg-type interactive --content …`。

自动化验证覆盖签名校验、修改业务字段、错误聊天及提交者、过期、并发重复提交、发送失败，以及 bridge 重启后恢复 Codex 会话。实际飞书客户端的展示和提交需要在运行新程序后验证。
