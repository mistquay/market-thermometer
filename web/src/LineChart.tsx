import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { LineChart as ELine } from 'echarts/charts'
import { DataZoomComponent, GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { Point } from './api'
import { useTheme } from './theme'

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
  const { theme } = useTheme()

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
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const accent = cssVar('--accent')
    chart.current?.setOption(
      {
        animation: false,
        grid: { left: 60, right: 24, top: 18, bottom: 60 },
        tooltip: {
          trigger: 'axis',
          backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
          borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
          borderWidth: 1,
          textStyle: { color: inkMain, fontSize: 12 },
          valueFormatter: (v: unknown) => (typeof v === 'number' ? format(v) : String(v)),
        },
        xAxis: {
          type: 'category',
          data: points.map((p) => p.date),
          boundaryGap: false,
          axisLine: { lineStyle: { color: line } },
          axisTick: { show: false },
          axisLabel: { color: ink, fontSize: 11 },
        },
        yAxis: {
          type: 'value',
          scale: true,
          splitLine: { lineStyle: { color: line, type: 'dashed' } },
          axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => format(v) },
        },
        dataZoom: [
          { type: 'inside' },
          { type: 'slider', height: 18, bottom: 12, borderColor: cssVar('--line'), textStyle: { color: ink } },
        ],
        series: [
          {
            name,
            type: 'line',
            data: points.map((p) => p.value),
            showSymbol: false,
            sampling: 'lttb',
            lineStyle: { width: 1.8, color: accent },
            itemStyle: { color: accent },
            areaStyle: {
              color: {
                type: 'linear',
                x: 0,
                y: 0,
                x2: 0,
                y2: 1,
                colorStops: [
                  { offset: 0, color: theme === 'dark' ? 'rgba(59, 130, 246, 0.20)' : 'rgba(37, 99, 235, 0.14)' },
                  { offset: 1, color: 'rgba(59, 130, 246, 0.00)' },
                ],
              },
            },
          },
        ],
      },
      { notMerge: true },
    )
  }, [points, name, format, theme])

  return <div ref={el} className="chart" />
}
