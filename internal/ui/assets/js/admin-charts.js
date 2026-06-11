// admin-charts.js — Chart.js render helpers for the admin dashboard.
//
// Charts are server-driven: the templ partial emits a <canvas> with the
// normalized trend baked into a data-trend attribute, then calls the matching
// render function. The function is idempotent — it destroys any existing chart
// bound to the canvas before drawing, so it is safe to re-run after an htmx
// swap (the 7/30/90-day toggles replace the whole card, scripts and all).
//
// Values are normalized magnitudes; the y-axis is hidden because the real figure
// lives in each point's tooltip Sub label. Two kinds are selected via the canvas
// data-kind attribute: "line" plots non-negative mags in [0,1] as a filled line
// (revenue, running totals); "delta" plots signed mags in [-1,1] as a
// zero-centered bar chart (gains teal/up, losses rust/down). Either way
// charts.ChartPoint maps straight in — no handler changes.

(function () {
	// Admin palette (mirrors the rr-* overrides in layouts/admin.templ).
	var INK = "#0E0D0C";
	var RUST = "#B4351D";
	var AMBER = "#F2A03D";
	var MUTED = "#6B6862";
	var BORDER = "#E5E2DA";
	var TEAL = "#0A7870"; // net gain (delta bars up)
	var ZERO = "rgba(14,13,12,0.22)"; // zero baseline rule for delta bars
	var FONT = "'Inter', system-ui, -apple-system, 'Segoe UI', sans-serif";

	// Vertical amber glow under the line — the "candle amber" highlight, low
	// intensity so it reads as a fill, not a billboard. Falls back to a flat
	// tint before the chart area is measured on first paint.
	function amberFill(ctx) {
		var chart = ctx.chart;
		var area = chart.chartArea;
		if (!area) return "rgba(242,160,61,0.12)";
		var g = chart.ctx.createLinearGradient(0, area.top, 0, area.bottom);
		g.addColorStop(0, "rgba(242,160,61,0.30)");
		g.addColorStop(1, "rgba(242,160,61,0.00)");
		return g;
	}

	// xScale builds the shared date x-axis, thinned to ~8 ticks so 90-day windows
	// don't crowd. No gridlines; the axis border is the only horizontal rule.
	function xScale(d) {
		return {
			grid: { display: false },
			border: { color: BORDER },
			ticks: {
				color: MUTED,
				maxTicksLimit: Math.min(8, d.labels.length),
				maxRotation: 0,
				autoSkip: true,
				font: { family: FONT, size: 11 },
			},
		};
	}

	// tooltipCfg builds the shared ink tooltip: date as title, the pre-formatted
	// Sub string (currency, count, or signed delta) as the body.
	function tooltipCfg() {
		return {
			backgroundColor: INK,
			titleColor: "#F6EFE1",
			bodyColor: "#FFFFFF",
			borderColor: BORDER,
			borderWidth: 1,
			cornerRadius: 2,
			padding: 8,
			displayColors: false,
			titleFont: { family: FONT, size: 11, weight: "normal" },
			bodyFont: { family: FONT, size: 12, weight: "bold" },
			callbacks: {
				label: function (item) {
					var subs = item.dataset.subs || [];
					return subs[item.dataIndex] || "";
				},
			},
		};
	}

	// renderLine draws a filled line of non-negative magnitudes in [0,1].
	function renderLine(el, d) {
		new Chart(el, {
			type: "line",
			data: {
				labels: d.labels,
				datasets: [
					{
						data: d.mags,
						subs: d.subs, // carried for the tooltip callback
						borderColor: RUST,
						borderWidth: 2,
						fill: true,
						backgroundColor: amberFill,
						tension: 0, // straight segments between points, not curves
						pointRadius: 0,
						pointHoverRadius: 4,
						pointHoverBackgroundColor: RUST,
						pointHoverBorderColor: "#FFFFFF",
						pointHoverBorderWidth: 1.5,
					},
				],
			},
			options: {
				responsive: true,
				maintainAspectRatio: false,
				animation: false, // admin: motion is utility, not theatre
				interaction: { mode: "index", intersect: false },
				layout: { padding: { top: 8, bottom: 0 } },
				scales: {
					y: { min: 0, max: 1, display: false, grace: "5%" },
					x: xScale(d),
				},
				plugins: { legend: { display: false }, tooltip: tooltipCfg() },
			},
		});
	}

	// renderDelta draws signed magnitudes in [-1,1] as a zero-centered bar chart:
	// net gains rise in teal, net losses fall in rust, with a single rule at zero.
	function renderDelta(el, d) {
		var colors = d.mags.map(function (m) {
			return m >= 0 ? TEAL : RUST;
		});
		// Hide the x-axis border so the zero baseline is the only horizontal rule.
		var xs = xScale(d);
		xs.border = { display: false };

		new Chart(el, {
			type: "bar",
			data: {
				labels: d.labels,
				datasets: [
					{
						data: d.mags,
						subs: d.subs,
						backgroundColor: colors,
						borderWidth: 0,
						categoryPercentage: 0.9,
						barPercentage: 0.9,
					},
				],
			},
			options: {
				responsive: true,
				maintainAspectRatio: false,
				animation: false,
				interaction: { mode: "index", intersect: false },
				layout: { padding: { top: 8, bottom: 0 } },
				scales: {
					y: {
						min: -1,
						max: 1,
						ticks: { display: false }, // value lives in the tooltip
						border: { display: false },
						grid: {
							// Draw the zero baseline only; everything else transparent.
							drawTicks: false,
							color: function (c) {
								return c.tick && c.tick.value === 0 ? ZERO : "transparent";
							},
							lineWidth: function (c) {
								return c.tick && c.tick.value === 0 ? 1 : 0;
							},
						},
					},
					x: xs,
				},
				plugins: { legend: { display: false }, tooltip: tooltipCfg() },
			},
		});
	}

	// barValuePlugin draws each bar's primary value just past its end, so the
	// figure stays visible without a hidden x-axis or a tooltip. The x-scale is
	// padded past the leader (max 1.25) to leave room for these labels.
	var barValuePlugin = {
		id: "barValues",
		afterDatasetsDraw: function (chart) {
			var ds = chart.data.datasets[0];
			var values = ds.values || [];
			var meta = chart.getDatasetMeta(0);
			var c = chart.ctx;
			c.save();
			c.font = "600 12px " + FONT;
			c.fillStyle = MUTED;
			c.textAlign = "left";
			c.textBaseline = "middle";
			meta.data.forEach(function (bar, i) {
				var label = values[i];
				if (!label) return;
				c.fillText(label, bar.x + 6, bar.y);
			});
			c.restore();
		},
	};

	// barTooltipCfg reuses the shared ink tooltip but joins the primary value and
	// the secondary sub (e.g. "248 units · $1,240") into one body line.
	function barTooltipCfg() {
		var cfg = tooltipCfg();
		cfg.callbacks = {
			label: function (item) {
				var v = item.dataset.values || [];
				var s = item.dataset.subs || [];
				var parts = [];
				if (v[item.dataIndex]) parts.push(v[item.dataIndex]);
				if (s[item.dataIndex]) parts.push(s[item.dataIndex]);
				return parts.join(" · ");
			},
		};
		return cfg;
	}

	// renderBars draws a horizontal bar chart: row labels on the y-axis, bar
	// length = magnitude (relative to the leader), ink bars that flush rust on
	// hover. The primary value rides at each bar's end (barValuePlugin); the
	// secondary value lands in the tooltip.
	function renderBars(el, d) {
		new Chart(el, {
			type: "bar",
			data: {
				labels: d.labels,
				datasets: [
					{
						data: d.mags,
						values: d.values, // carried for the value plugin
						subs: d.subs, // carried for the tooltip callback
						backgroundColor: INK,
						hoverBackgroundColor: RUST,
						borderWidth: 0,
						barThickness: 16,
						maxBarThickness: 22,
					},
				],
			},
			options: {
				indexAxis: "y",
				responsive: true,
				maintainAspectRatio: false,
				animation: false,
				interaction: { mode: "index", intersect: false },
				layout: { padding: { right: 8 } },
				scales: {
					x: { min: 0, max: 1.25, display: false, grid: { display: false } },
					y: {
						grid: { display: false },
						border: { display: false },
						ticks: {
							color: INK,
							crossAlign: "far",
							font: { family: FONT, size: 12, weight: "600" },
							callback: function (v) {
								var lbl = this.getLabelForValue(v);
								return lbl.length > 24 ? lbl.slice(0, 23) + "…" : lbl;
							},
						},
					},
				},
				plugins: { legend: { display: false }, tooltip: barTooltipCfg() },
			},
			plugins: [barValuePlugin],
		});
	}

	// renderBarsCanvas parses a data-bars canvas and draws it.
	function renderBarsCanvas(el) {
		if (!el || !el.dataset.bars) return;
		var d;
		try {
			d = JSON.parse(el.dataset.bars);
		} catch (e) {
			return;
		}
		renderBars(el, d);
	}

	// renderOne dispatches on the canvas data-kind: "delta" → bar, else line.
	function renderOne(el) {
		if (!el || !el.dataset.trend) return;
		var d;
		try {
			d = JSON.parse(el.dataset.trend);
		} catch (e) {
			return;
		}
		if (el.dataset.kind === "delta") {
			renderDelta(el, d);
		} else {
			renderLine(el, d);
		}
	}

	// rrRenderTrends draws every trend canvas on the page that hasn't been drawn
	// yet, skipping any that already carries a Chart instance. Safe to call on
	// initial load and after each htmx swap: the swapped-in canvas is fresh (no
	// instance) so it renders, while untouched sibling charts are left alone.
	window.rrRenderTrends = function () {
		if (!window.Chart) return;
		var nodes = document.querySelectorAll("canvas[data-trend], canvas[data-bars]");
		for (var i = 0; i < nodes.length; i++) {
			if (Chart.getChart(nodes[i])) continue; // already drawn
			if (nodes[i].dataset.bars) renderBarsCanvas(nodes[i]);
			else renderOne(nodes[i]);
		}
	};

	// Draw on initial load and after every htmx swap. htmx:load fires for any
	// newly inserted content, so toggling the 7/30/90-day selector (which
	// replaces the whole card via outerHTML) re-draws the fresh canvas — the
	// inline-script-in-fragment approach doesn't survive htmx swaps reliably.
	function boot() {
		window.rrRenderTrends();
	}
	if (document.readyState !== "loading") boot();
	else document.addEventListener("DOMContentLoaded", boot);
	document.addEventListener("htmx:load", boot);
})();
