// Shows the panel's public status page. It needs the status page to be
// enabled; when it cannot be read, the section keeps its note and a link.
(function () {
  var box = document.querySelector("[data-status-api]");
  if (!box) return;
  var api = box.getAttribute("data-status-api");
  var url = box.getAttribute("data-status-url");
  var overall = box.querySelector("[data-overall]");
  var list = box.querySelector("[data-components]");
  var note = box.querySelector("[data-status-note]");
  var labels = { operational: "Online", degraded: "Degraded", partial_outage: "Partial outage", major_outage: "Offline", maintenance: "Maintenance" };
  function label(state) { return labels[state] || state || "Unknown"; }
  function unavailable() {
    overall.textContent = "unavailable";
    overall.className = "overall";
    if (url) {
      note.textContent = "";
      var a = document.createElement("a");
      a.href = url; a.textContent = "Open the status page";
      note.appendChild(a);
    }
  }
  if (!api) { unavailable(); return; }
  fetch(api, { credentials: "omit" }).then(function (r) {
    if (!r.ok) throw new Error(String(r.status));
    return r.json();
  }).then(function (s) {
    overall.textContent = label(s.overall);
    overall.className = "overall " + (s.overall || "");
    list.textContent = "";
    (s.components || []).forEach(function (c) {
      var li = document.createElement("li");
      var name = document.createElement("span");
      name.textContent = c.name;
      var st = document.createElement("span");
      st.className = "state " + (c.state || "");
      st.textContent = label(c.state);
      li.appendChild(name); li.appendChild(st);
      list.appendChild(li);
    });
    note.textContent = "";
    if (url) {
      var a = document.createElement("a");
      a.href = url; a.textContent = "Full status and history";
      note.appendChild(a);
    }
  }).catch(unavailable);
})();
