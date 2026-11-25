"""市场温度计外部插件 SDK（单文件，只用 Python 标准库）。

协议：标准输入输出上按行分隔的 JSON-RPC 2.0（见 docs/plugin-protocol.md）。
- stdout 只能输出协议消息：SDK 会把 print() 等普通输出重定向到 stderr，宿主把 stderr 当作插件日志；
- 输入输出一律按 UTF-8 处理（Windows 默认的 GBK 会把中文弄乱）。

用法：

    from mtplugin import Plugin, PluginError

    plugin = Plugin()

    @plugin.method("metric.fetch")
    def fetch(metric, from_, to):
        return {"points": [{"date": "2026-01-02", "value": 1.0}]}

    plugin.run()

参数名与协议字段同名；Python 关键字（如 from）在后面加下划线。
"""

import json
import keyword
import sys
import traceback

PROTOCOL = 1


class PluginError(Exception):
    """业务错误：作为 JSON-RPC error 返回给宿主，不会导致插件被重启。"""

    def __init__(self, message, code=-32000):
        super().__init__(message)
        self.code = code


class Plugin:
    def __init__(self):
        self.config = {}
        self._handlers = {}
        self._on_init = None
        self._out = sys.stdout.buffer
        sys.stdout = sys.stderr  # 普通 print 不会污染协议通道

    def method(self, name):
        """注册一个方法处理函数。"""

        def deco(fn):
            self._handlers[name] = fn
            return fn

        return deco

    def on_initialize(self, fn):
        """注册初始化回调：收到配置后调用一次，可在此校验配置（抛出 PluginError 表示配置不对）。"""
        self._on_init = fn
        return fn

    def log(self, *args):
        print(*args, file=sys.stderr, flush=True)

    def _send(self, msg):
        self._out.write(json.dumps(msg, ensure_ascii=False).encode("utf-8") + b"\n")
        self._out.flush()

    def _dispatch(self, method, params):
        if method == "initialize":
            if (params or {}).get("protocol") != PROTOCOL:
                raise PluginError("协议版本不匹配：宿主 %s，插件 %s" % ((params or {}).get("protocol"), PROTOCOL))
            self.config = (params or {}).get("config") or {}
            if self._on_init:
                self._on_init(self.config)
            return {}
        if method == "shutdown":
            return {}
        fn = self._handlers.get(method)
        if fn is None:
            raise PluginError("不支持的方法：" + method, code=-32601)
        kwargs = {(k + "_" if keyword.iskeyword(k) else k): v for k, v in (params or {}).items()}
        return fn(**kwargs)

    def run(self):
        for raw in sys.stdin.buffer:
            line = raw.decode("utf-8").strip()
            if not line:
                continue
            try:
                req = json.loads(line)
            except ValueError:
                self.log("无法解析的请求：", line[:200])
                continue
            rid, method = req.get("id"), req.get("method", "")
            try:
                result = self._dispatch(method, req.get("params"))
                self._send({"jsonrpc": "2.0", "id": rid, "result": result if result is not None else {}})
            except PluginError as e:
                self._send({"jsonrpc": "2.0", "id": rid, "error": {"code": e.code, "message": str(e)}})
            except Exception as e:  # 插件自身的 bug：返回错误并把堆栈写进日志
                traceback.print_exc(file=sys.stderr)
                self._send({"jsonrpc": "2.0", "id": rid, "error": {"code": -32603, "message": "%s: %s" % (type(e).__name__, e)}})
            if method == "shutdown":
                return
