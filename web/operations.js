const { api, shortID, setMessage, withBusy } = window.LockFlow;
const form = document.querySelector("#plan-form");
const message = document.querySelector("#plan-message");
let latestChambers = [];

async function refresh() {
  const [plans, chambers, health] = await Promise.all([api("/api/plans"), api("/api/chambers"), api("/healthz")]);
  latestChambers = chambers;
  document.querySelector("#health").textContent = health.status === "ok" ? "Devices online" : "Attention required";
  const rows = Object.entries(plans).flatMap(([chamber, items]) => items.map((plan) => `<tr><td>${chamber}</td><td>${shortID(plan.id)}</td><td>${plan.direction}</td><td>${plan.vessels.length}</td><td>${new Date(plan.created_at).toLocaleTimeString()}</td></tr>`));
  document.querySelector("#plans").innerHTML = rows.join("") || '<tr><td colspan="5" class="muted">No active plans</td></tr>';
  const current = chambers.find((item) => item.cycle);
  document.querySelector("#metric-chamber").textContent = current?.chamber_id || "Idle";
  document.querySelector("#metric-stage").textContent = current?.cycle?.stage || "-";
  document.querySelector("#metric-cycle").textContent = shortID(current?.cycle?.id);
  const levels = await Promise.all(chambers.map((item) => api(`/api/chambers/${item.chamber_id}/levels`)));
  renderLevels(chambers, levels);
}

function renderLevels(chambers, levels) {
  const host = document.querySelector("#levels");
  host.innerHTML = chambers.map((item, index) => {
    const status = levels[index];
    const verdict = status.verdict || {};
    const policy = status.policy || {};
    const reasons = verdict.reasons || [];
    const badge = verdict.stable
      ? '<span class="status">Stable — clear to release</span>'
      : '<span class="status warning">Delayed — not cleared to release</span>';
    const basis = `Samples ${verdict.sample_count || 0}/${policy.min_samples} · span ${(verdict.span_seconds || 0).toFixed(0)}s/≥${policy.min_span_seconds}s · fluctuation ${(verdict.fluctuation_m || 0).toFixed(3)}m/≤${policy.fluctuation_m}m · max age ${policy.max_sample_age_seconds}s`;
    const reasonList = reasons.length
      ? `<ul class="reasons">${reasons.map((reason) => `<li>${reason}</li>`).join("")}</ul>`
      : '<div class="muted">All stability conditions met.</div>';
    const samples = (status.samples || []).slice().reverse();
    const sampleRows = samples.map((sample) => `<tr><td>${new Date(sample.observed_at).toLocaleTimeString()}</td><td>${sample.meters.toFixed(3)} m</td></tr>`).join("");
    const sampleTable = sampleRows
      ? `<table><thead><tr><th>Observed</th><th>Level</th></tr></thead><tbody>${sampleRows}</tbody></table>`
      : '<div class="muted">No samples recorded for the current transit.</div>';
    const recorder = item.cycle
      ? `<div class="form-row sample-form"><div><label for="meters-${item.chamber_id}">Level (m)</label><input id="meters-${item.chamber_id}" type="number" step="0.001" min="0" value="6.000"></div><div><label>&nbsp;</label><button data-record="${item.chamber_id}">Record sample</button></div></div>`
      : '<div class="muted">No active transit — sampling closed.</div>';
    return `<div class="level-block"><h3>${item.chamber_id} ${badge}</h3><div class="muted">${basis}</div>${reasonList}${sampleTable}${recorder}</div>`;
  }).join("");
  host.querySelectorAll("[data-record]").forEach((button) => button.addEventListener("click", () => recordSample(button.dataset.record, button)));
}

async function recordSample(chamberID, button) {
  const chamber = latestChambers.find((item) => item.chamber_id === chamberID);
  const input = document.querySelector(`#meters-${chamberID}`);
  await withBusy(button, async () => {
    try {
      const verdict = await api(`/api/chambers/${chamberID}/levels`, { method: "POST", body: JSON.stringify({
        cycle_id: chamber.cycle.id,
        generation: chamber.cycle.generation,
        meters: Number(input.value),
      }) });
      setMessage(message, verdict.stable
        ? `${chamberID}: level stable — clear to release.`
        : `${chamberID}: not stable — ${(verdict.reasons || []).join("; ")}`);
      await refresh();
    } catch (error) { setMessage(message, error.message); }
  });
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = form.querySelector("button");
  await withBusy(button, async () => {
    try {
      const result = await api("/api/plans", { method: "POST", body: JSON.stringify({
        chamber_id: document.querySelector("#chamber").value,
        direction: document.querySelector("#direction").value,
        vessels: [{ name: document.querySelector("#vessel").value, call_sign: document.querySelector("#call-sign").value, length_m: Number(document.querySelector("#length").value) }],
      }) });
      setMessage(message, `Plan ${shortID(result.plan.id)} admitted to ${result.plan.chamber_id}.`);
      await refresh();
    } catch (error) { setMessage(message, error.message); }
  });
});

refresh().catch((error) => setMessage(message, error.message));
setInterval(() => refresh().catch(() => {}), 5000);
