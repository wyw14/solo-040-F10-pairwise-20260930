const { api, shortID, setMessage, withBusy } = window.LockFlow;
const form = document.querySelector("#plan-form");
const message = document.querySelector("#plan-message");
const levelHost = document.querySelector("#level-panels");

const fmtSeconds = (ms) => (ms === undefined || ms === null) ? "-" : `${(ms / 1000).toFixed(1)}s`;
const escapeHTML = (value) => String(value ?? "").replace(/[&<>"']/g, (ch) => (
  { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch]
));

function levelPanelHTML(item) {
  const level = item.level || {};
  const config = level.config || {};
  const releasing = level.decision === "release";
  const stage = item.cycle?.stage || "idle";
  const badge = releasing
    ? '<span class="verdict release">RELEASE</span>'
    : '<span class="verdict hold">HOLD</span>';
  const reason = level.reason
    ? `<div class="verdict-reason">${escapeHTML(level.reason)}</div>`
    : '<div class="verdict-reason muted">No blocking reason recorded</div>';
  const samples = (level.recent_samples || []).slice(-6).map((sample) => `
    <tr>
      <td>${new Date(sample.observed_at).toLocaleTimeString()}</td>
      <td>${Number(sample.meters).toFixed(3)}</td>
      <td class="quality ${escapeHTML(sample.quality)}">${escapeHTML(sample.quality)}</td>
    </tr>`).join("");
  const gateButtons = (stage === "sealed" || stage === "leveling")
    ? `<div class="form-row"><button data-open="${item.chamber_id}" data-direction="upstream">Open upstream gate</button><button data-open="${item.chamber_id}" data-direction="downstream">Open downstream gate</button></div>`
    : "";
  const configEditor = `
    <details class="config-editor"><summary>Chamber stability config</summary>
      <form data-config="${item.chamber_id}">
        <div class="form-row four">
          <div><label>Min samples</label><input type="number" min="2" max="64" name="minimum_samples" value="${config.minimum_samples ?? 4}"></div>
          <div><label>Min span (ms)</label><input type="number" min="0" name="minimum_span_ms" value="${config.minimum_span_ms ?? 3000}"></div>
          <div><label>Swing (m)</label><input type="number" step="0.001" min="0.001" name="swing_tolerance_m" value="${config.swing_tolerance_m ?? 0.05}"></div>
          <div><label>Max age (ms)</label><input type="number" min="1" name="max_observation_age_ms" value="${config.max_observation_age_ms ?? 10000}"></div>
        </div>
        <button type="submit">Save config</button>
      </form>
      <div class="message"></div>
    </details>`;
  return `
  <section class="panel level-card" data-chamber="${item.chamber_id}">
    <h3>${item.chamber_id} <span class="muted">/ ${stage} / cycle ${shortID(item.cycle?.id)}</span> ${badge}</h3>
    ${reason}
    <div class="metrics">
      <div class="metric">Samples<strong>${level.sample_count ?? 0}/${config.minimum_samples ?? "-"}</strong></div>
      <div class="metric">Span<strong>${fmtSeconds(level.span_ms)} / ${fmtSeconds(config.minimum_span_ms)}</strong></div>
      <div class="metric">Swing<strong>${level.swing_m !== undefined ? Number(level.swing_m).toFixed(3) : "-"} / ${config.swing_tolerance_m ?? "-"}m</strong></div>
      <div class="metric">Latest age<strong>${fmtSeconds(level.age_ms)} / ${fmtSeconds(config.max_observation_age_ms)}</strong></div>
    </div>
    <table><thead><tr><th>Observed</th><th>Meters</th><th>Quality</th></tr></thead>
      <tbody>${samples || '<tr><td colspan="3" class="muted">No samples this transit</td></tr>'}</tbody></table>
    <form class="sample-form" data-chamber="${item.chamber_id}">
      <div class="form-row">
        <div><label>Meters</label><input required type="number" step="0.001" name="meters" value="${level.latest_meters ?? 5}"></div>
        <div><label>Quality</label><select name="quality"><option value="ok">ok</option><option value="abnormal">abnormal</option><option value="missing">missing</option></select></div>
        <div><label>&nbsp;</label><button type="submit">Submit sample</button></div>
      </div>
      <div class="message"></div>
    </form>
    ${gateButtons}
    ${configEditor}
  </section>`;
}

async function openGate(chamberID, direction, button) {
  await withBusy(button, async () => {
    try {
      await api(`/api/chambers/${chamberID}/gates/${direction}/open`, { method: "POST", body: "{}" });
      await refresh();
    } catch (error) {
      const card = button.closest(".level-card");
      setMessage(card?.querySelector(".message"), error.message);
    }
  });
}

async function submitSample(chamberID, formElement) {
  const button = formElement.querySelector("button");
  await withBusy(button, async () => {
    try {
      const payload = {
        meters: Number(formElement.meters.value),
        quality: formElement.quality.value,
      };
      await api(`/api/chambers/${chamberID}/level/samples`, { method: "POST", body: JSON.stringify(payload) });
      setMessage(formElement.querySelector(".message"), "");
      await refresh();
    } catch (error) {
      setMessage(formElement.querySelector(".message"), error.message);
    }
  });
}

async function refresh() {
  const [plans, chambers, health] = await Promise.all([api("/api/plans"), api("/api/chambers"), api("/healthz")]);
  document.querySelector("#health").textContent = health.status === "ok" ? "Devices online" : "Attention required";
  const rows = Object.entries(plans).flatMap(([chamber, items]) => items.map((plan) => `<tr><td>${chamber}</td><td>${shortID(plan.id)}</td><td>${plan.direction}</td><td>${plan.vessels.length}</td><td>${new Date(plan.created_at).toLocaleTimeString()}</td></tr>`));
  document.querySelector("#plans").innerHTML = rows.join("") || '<tr><td colspan="5" class="muted">No active plans</td></tr>';
  const current = chambers.find((item) => item.cycle);
  document.querySelector("#metric-chamber").textContent = current?.chamber_id || "Idle";
  document.querySelector("#metric-stage").textContent = current?.cycle?.stage || "-";
  document.querySelector("#metric-cycle").textContent = shortID(current?.cycle?.id);

  levelHost.innerHTML = chambers.map(levelPanelHTML).join("");
  levelHost.querySelectorAll(".sample-form").forEach((element) => {
    element.addEventListener("submit", (event) => {
      event.preventDefault();
      submitSample(element.dataset.chamber, element);
    });
  });
  levelHost.querySelectorAll("[data-open]").forEach((button) => {
    button.addEventListener("click", () => openGate(button.dataset.open, button.dataset.direction, button));
  });
  levelHost.querySelectorAll("[data-config]").forEach((element) => {
    element.addEventListener("submit", (event) => {
      event.preventDefault();
      const chamberID = element.dataset.config;
      const button = element.querySelector("button");
      withBusy(button, async () => {
        try {
          await api(`/api/chambers/${chamberID}/level/config`, { method: "PUT", body: JSON.stringify({
            minimum_samples: Number(element.minimum_samples.value),
            minimum_span_ms: Number(element.minimum_span_ms.value),
            swing_tolerance_m: Number(element.swing_tolerance_m.value),
            max_observation_age_ms: Number(element.max_observation_age_ms.value),
          }) });
          await refresh();
        } catch (error) {
          setMessage(element.parentElement.querySelector(".message"), error.message);
        }
      });
    });
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
