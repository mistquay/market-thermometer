"""示例外部插件：从本地 CSV 文件读取指标（也可用于手工补数、导入付费数据的导出文件）。

CSV 格式（UTF-8，带表头）：date,metric,value，日期为 YYYY-MM-DD。
plugin.json 里声明本插件提供哪些指标；CSV 中其他指标会被忽略。
"""

import csv
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "sdk"))
from mtplugin import Plugin, PluginError  # noqa: E402

plugin = Plugin()
HERE = os.path.dirname(os.path.abspath(__file__))


def csv_path():
    path = plugin.config.get("path") or "sample.csv"
    return path if os.path.isabs(path) else os.path.join(HERE, path)


@plugin.on_initialize
def check(config):
    if not os.path.exists(csv_path()):
        raise PluginError("CSV 文件不存在：" + csv_path())
    plugin.log("csv_source 已就绪：", csv_path())


@plugin.method("metric.fetch")
def fetch(metric, from_, to):
    points = []
    with open(csv_path(), encoding="utf-8-sig", newline="") as f:
        for row in csv.DictReader(f):
            if row.get("metric") != metric:
                continue
            d = (row.get("date") or "").strip()
            if from_ <= d <= to:
                try:
                    points.append({"date": d, "value": float(row["value"])})
                except (TypeError, ValueError):
                    raise PluginError("第 %d 行数值不合法：%r" % (len(points) + 2, row.get("value")))
    return {"points": points}


if __name__ == "__main__":
    plugin.run()
