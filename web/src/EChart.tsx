import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { BarChart, CandlestickChart, HeatmapChart, LineChart, ScatterChart } from 'echarts/charts'
import {
  AxisPointerComponent,
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  MarkAreaComponent,
  MarkLineComponent,
  MarkPointComponent,
  TooltipComponent,
  VisualMapComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([
  LineChart,
  HeatmapChart,
  CandlestickChart,
  BarChart,
  ScatterChart,
  VisualMapComponent,
  GridComponent,
  TooltipComponent,
  DataZoomComponent,
  LegendComponent,
  MarkLineComponent,
  MarkPointComponent,
  MarkAreaComponent,
  AxisPointerComponent,
  CanvasRenderer,
])

export type EChartsOption = echarts.EChartsCoreOption

export function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

/** 通用 ECharts 容器：option 变化时整体替换（notMerge），尺寸严格跟随容器宽度，防止初始窄宽坍塌。 */
export default function EChart({ option, height }: { option: EChartsOption; height: number }) {
  const el = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts | null>(null)

  useEffect(() => {
    const node = el.current
    if (!node) return
    const c = echarts.init(node)
    chart.current = c
    const doResize = () => {
      if (!el.current || !chart.current) return
      const w = el.current.clientWidth
      if (w > 0) chart.current.resize({ width: w, height })
    }
    const ro = new ResizeObserver(doResize)
    ro.observe(node)
    const raf1 = requestAnimationFrame(doResize)
    const timer = window.setTimeout(doResize, 120)
    return () => {
      cancelAnimationFrame(raf1)
      window.clearTimeout(timer)
      ro.disconnect()
      c.dispose()
      chart.current = null
    }
  }, [height])

  useEffect(() => {
    const c = chart.current
    if (!c) return
    c.setOption(option, { notMerge: true })
    const raf = requestAnimationFrame(() => {
      if (el.current && chart.current && el.current.clientWidth > 0) {
        if (Math.abs(chart.current.getWidth() - el.current.clientWidth) > 2) {
          chart.current.resize({ width: el.current.clientWidth, height })
        }
      }
    })
    return () => cancelAnimationFrame(raf)
  }, [option, height])

  return <div ref={el} className="w-full min-w-0" style={{ height, width: '100%' }} />
}
