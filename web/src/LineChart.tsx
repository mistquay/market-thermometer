import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { LineChart as ELine } from 'echarts/charts'
import { DataZoomComponent, GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { Point } from './api'

echarts.use([ELine, GridComponent, TooltipComponent, DataZoomComponent, CanvasRenderer])

type Props = {
  points: Point[]
  name: string
  format: (v: number) => string
}

function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

export default function LineChart({ points, name, format }: Props) {
  const el = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts | null>(null)

  useEffect(() => {
    const c = echarts.init(el.current!)
    chart.current = c
    const ro = new ResizeObserver(() => c.resize())
    ro.observe(el.current!)
    return () => {
      ro.disconnect()
      c.dispose()
      chart.current = null
    }
  }, [])

  useEffect(() => {
    const ink = cssVar('--ink-2')
    const line = cssVar('--line')
    const accent = cssVar('--accent')
    chart.current?.setOption(
      {
        animation: false,
        grid: { left: 64, right: 24, top: 16, bottom: 64 },
        tooltip: {
          trigger: 'axis',
          valueFormatter: (v: unknown) => (typeof v === 'number' ? format(v) : String(v)),
        },
        xAxis: {
          type: 'category',
          data: points.map((p) => p.date),
          axisLine: { lineStyle: { color: line } },
          axisLabel: { color: ink },
        },
        yAxis: {
          type: 'value',
          scale: true,
          splitLine: { lineStyle: { color: line } },
          axisLabel: { color: ink, formatter: (v: number) => format(v) },
        },
        dataZoom: [
          { type: 'inside' },
          { type: 'slider', height: 22, bottom: 16, borderColor: line, textStyle: { color: ink } },
        ],
        series: [
          {
            name,
            type: 'line',
            data: points.map((p) => p.value),
            showSymbol: false,
            sampling: 'lttb',
            lineStyle: { width: 1.5, color: accent },
            itemStyle: { color: accent },
          },
        ],
      },
      { notMerge: true },
    )
  }, [points, name, format])

  return <div ref={el} className="chart" />
}
