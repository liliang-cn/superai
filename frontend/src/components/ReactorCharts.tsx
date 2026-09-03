import React, { useEffect, useMemo, useRef } from "react";
import * as echarts from "echarts";
import { Snap, fmtK } from "./Reactor";

/**
 * The charts beside the wheel.
 *
 * Each one is the shape its figure wants: a share is a ring, a series of
 * turns is columns, a rate over two minutes is an area, a level is a gauge.
 * All of them glow, because they sit next to a reactor and a flat chart
 * beside a glowing one looks like it belongs to a different page. The glow is
 * ECharts' own shadowBlur on the series, in the same four signal colours the
 * wheel uses, so a colour means the same thing on both.
 *
 * They read the same pushed snapshot the wheel does. One feed, several views.
 */

const CYAN = "#5ee0ff", AMBER = "#ffb547", LIME = "#9dff6a", ROSE = "#ff5c7a", VIOLET = "#b87aff", PINK = "#ff73d9", TEAL = "#4dffdb";
const DIM = "#6b7690", LINE = "#1a2340", MONO = "ui-monospace, Menlo, monospace";

function glow(color: string, blur = 14) {
  return { color, shadowBlur: blur, shadowColor: color };
}

/** One chart, kept alive across renders and resized with its box. */
function Chart({ option, height = "100%" }: { option: echarts.EChartsOption; height?: number | string }) {
  const ref = useRef<HTMLDivElement>(null);
  const inst = useRef<echarts.ECharts | null>(null);
  useEffect(() => {
    if (!ref.current) return;
    inst.current = echarts.init(ref.current, undefined, { renderer: "canvas" });
    const ro = new ResizeObserver(() => inst.current?.resize());
    ro.observe(ref.current);
    return () => {
      ro.disconnect();
      inst.current?.dispose();
      inst.current = null;
    };
  }, []);
  useEffect(() => {
    // notMerge off: series update in place so the bars slide and the ring
    // turns, rather than the whole chart being redrawn from nothing.
    inst.current?.setOption({ animationDuration: 500, animationDurationUpdate: 500, ...option }, { notMerge: false, lazyUpdate: true });
  }, [option]);
  return <div ref={ref} className="rx-chart" style={{ width: "100%", height, minHeight: 0 }} />;
}

const base: echarts.EChartsOption = {
  backgroundColor: "transparent",
  textStyle: { fontFamily: MONO, color: DIM },
  tooltip: { trigger: "item", backgroundColor: "#0c1120", borderColor: LINE, textStyle: { color: "#e6ebf7", fontFamily: MONO, fontSize: 11 } },
};

/** Cached against uncached tokens, and a second ring of turns. */
export function CacheRing({ snap }: { snap: Snap }) {
  const option = useMemo<echarts.EChartsOption>(() => {
    const cached = snap.cached, fresh = Math.max(0, snap.tokens - snap.cached);
    const pct = snap.tokens ? Math.round((100 * cached) / snap.tokens) : 0;
    return {
      ...base,
      title: { text: `${pct}%`, subtext: "cached", left: "center", top: "38%", textStyle: { color: AMBER, fontFamily: MONO, fontSize: 22, fontWeight: 800 }, subtextStyle: { color: DIM, fontFamily: MONO, fontSize: 9, letterSpacing: 2 } },
      series: [{
        type: "pie", radius: ["62%", "84%"], center: ["50%", "50%"], avoidLabelOverlap: false,
        label: { show: false }, labelLine: { show: false },
        itemStyle: { borderColor: "#05070f", borderWidth: 2 },
        // Nothing yet is an empty ring, not two halves of nothing.
        data: snap.tokens === 0 ? [{ value: 1, name: "none yet", itemStyle: { color: LINE } }] : [
          { value: cached, name: "cached", itemStyle: glow(AMBER) },
          { value: fresh, name: "fresh", itemStyle: glow(CYAN, 10) },
        ],
      }],
    };
  }, [snap.cached, snap.tokens]);
  return <Chart option={option} />;
}

/** What the calls did to the world. */
export function CallRing({ snap }: { snap: Snap }) {
  const option = useMemo<echarts.EChartsOption>(() => {
    const other = Math.max(0, snap.calls - snap.reads - snap.writes - snap.shells - snap.mcp - snap.memory);
    return {
      ...base,
      title: { text: String(snap.calls), subtext: "calls", left: "center", top: "38%", textStyle: { color: "#ffffff", fontFamily: MONO, fontSize: 22, fontWeight: 800 }, subtextStyle: { color: DIM, fontFamily: MONO, fontSize: 9, letterSpacing: 2 } },
      series: [{
        type: "pie", radius: ["62%", "84%"], center: ["50%", "50%"],
        label: { show: false }, labelLine: { show: false },
        itemStyle: { borderColor: "#05070f", borderWidth: 2 },
        data: [
          { value: snap.reads, name: "reads", itemStyle: glow(CYAN) },
          { value: snap.writes, name: "writes", itemStyle: glow(LIME) },
          { value: snap.shells, name: "shells", itemStyle: glow(AMBER) },
          { value: snap.mcp, name: "mcp", itemStyle: glow(PINK) },
          { value: snap.memory, name: "memory", itemStyle: glow(TEAL) },
          { value: other, name: "other", itemStyle: glow("#8a96b0", 6) },
          { value: snap.fails, name: "failed", itemStyle: glow(ROSE) },
        ].filter((d) => d.value > 0),
      }],
    };
  }, [snap.calls, snap.reads, snap.writes, snap.shells, snap.mcp, snap.memory, snap.fails]);
  return <Chart option={option} />;
}

/** Tokens per model turn, the last sixteen. */
export function TurnColumns({ snap }: { snap: Snap }) {
  const option = useMemo<echarts.EChartsOption>(() => {
    const turns = snap.events.filter((e) => e.kind === "model" && (e.n ?? 0) > 0).slice(-16);
    return {
      ...base,
      grid: { left: 40, right: 8, top: 10, bottom: 22 },
      xAxis: { type: "category", data: turns.map((e) => e.text.replace("round ", "r")), axisLine: { lineStyle: { color: LINE } }, axisTick: { show: false }, axisLabel: { color: DIM, fontSize: 9, fontFamily: MONO } },
      yAxis: { type: "value", splitLine: { lineStyle: { color: LINE } }, axisLabel: { color: DIM, fontSize: 9, fontFamily: MONO, formatter: (v: number) => fmtK(v) } },
      series: [{
        type: "bar", barMaxWidth: 18,
        data: turns.map((e) => e.n ?? 0),
        itemStyle: {
          borderRadius: [3, 3, 0, 0],
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: CYAN }, { offset: 1, color: "rgba(94,224,255,.15)" }]),
          shadowBlur: 16, shadowColor: CYAN,
        },
      }],
    };
  }, [snap.events]);
  return <Chart option={option} />;
}

/** Calls per tool, busiest at the top. */
export function ToolColumns({ snap }: { snap: Snap }) {
  const option = useMemo<echarts.EChartsOption>(() => {
    const top = snap.tools.slice(0, 8).reverse();
    return {
      ...base,
      grid: { left: 120, right: 30, top: 6, bottom: 6 },
      xAxis: { type: "value", show: false },
      yAxis: { type: "category", data: top.map((t) => t.name.replace(/^mcp_/, "")), axisLine: { show: false }, axisTick: { show: false }, axisLabel: { color: DIM, fontSize: 9.5, fontFamily: MONO, width: 112, overflow: "truncate" } },
      series: [{
        type: "bar", barMaxWidth: 12,
        label: { show: true, position: "right", color: "#e6ebf7", fontFamily: MONO, fontSize: 10 },
        data: top.map((t) => ({
          value: t.calls,
          itemStyle: {
            borderRadius: [0, 3, 3, 0],
            ...(t.errors ? glow(ROSE) : Date.now() - new Date(t.lastAt).getTime() < 3000 ? glow(AMBER, 18) : {
              color: new echarts.graphic.LinearGradient(0, 0, 1, 0, [{ offset: 0, color: "rgba(255,181,71,.25)" }, { offset: 1, color: AMBER }]),
              shadowBlur: 12, shadowColor: AMBER,
            }),
          },
        })),
      }],
    };
  }, [snap.tools]);
  return <Chart option={option} />;
}

/** Tokens and reasoning over the last two minutes, one point a second. */
export function BurnArea({ snap }: { snap: Snap }) {
  const option = useMemo<echarts.EChartsOption>(() => {
    const bins = snap.bins;
    return {
      ...base,
      tooltip: { ...base.tooltip, trigger: "axis" },
      grid: { left: 40, right: 8, top: 8, bottom: 18 },
      xAxis: { type: "category", data: bins.map((b) => new Date(b.sec * 1000).toTimeString().slice(3, 8)), axisLine: { lineStyle: { color: LINE } }, axisTick: { show: false }, axisLabel: { color: DIM, fontSize: 9, fontFamily: MONO, interval: 29 } },
      yAxis: [
        { type: "value", splitLine: { lineStyle: { color: LINE } }, axisLabel: { color: DIM, fontSize: 9, fontFamily: MONO, formatter: (v: number) => fmtK(v) } },
        { type: "value", show: false },
      ],
      series: [
        {
          name: "tokens", type: "line", smooth: 0.3, symbol: "none", data: bins.map((b) => b.tokens),
          lineStyle: { color: CYAN, width: 1.5, shadowBlur: 12, shadowColor: CYAN },
          areaStyle: { color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: "rgba(94,224,255,.45)" }, { offset: 1, color: "rgba(94,224,255,0)" }]) },
        },
        {
          name: "reasoning", type: "line", yAxisIndex: 1, smooth: 0.3, symbol: "none", data: bins.map((b) => b.think),
          lineStyle: { color: LIME, width: 1, shadowBlur: 10, shadowColor: LIME },
          areaStyle: { color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: "rgba(157,255,106,.25)" }, { offset: 1, color: "rgba(157,255,106,0)" }]) },
        },
      ],
    };
  }, [snap.bins]);
  return <Chart option={option} />;
}

/** CPU and heap, as two dials. */
export function LoadGauges({ snap }: { snap: Snap }) {
  const option = useMemo<echarts.EChartsOption>(() => {
    const heapMB = snap.heap / 1048576;
    const heapHi = Math.max(64, Math.ceil((snap.heapHigh / 1048576) * 1.25 / 32) * 32);
    const dial = (center: [string, string], color: string, value: number, max: number, name: string, fmt: (v: number) => string): echarts.GaugeSeriesOption => ({
      type: "gauge", center, radius: "80%", startAngle: 220, endAngle: -40, min: 0, max,
      progress: { show: true, width: 8, itemStyle: { color, shadowBlur: 16, shadowColor: color } },
      axisLine: { lineStyle: { width: 8, color: [[1, LINE]] } },
      axisTick: { show: false }, splitLine: { show: false }, axisLabel: { show: false }, pointer: { show: false },
      title: { offsetCenter: [0, "34%"], color: DIM, fontSize: 9, fontFamily: MONO },
      detail: { offsetCenter: [0, "0%"], color, fontSize: 16, fontWeight: 800, fontFamily: MONO, formatter: (v: number) => fmt(v) },
      data: [{ value, name }],
    });
    return {
      ...base,
      series: [
        dial(["27%", "55%"], VIOLET, Math.min(100, snap.cpu), 100, "CPU", (v) => v.toFixed(0) + "%"),
        dial(["73%", "55%"], CYAN, heapMB, heapHi, "HEAP MB", (v) => v.toFixed(0)),
      ],
    };
  }, [snap.cpu, snap.heap, snap.heapHigh]);
  return <Chart option={option} />;
}
