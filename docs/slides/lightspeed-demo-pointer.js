// Recording aid: follows real mouse events; does not automate or log actions.
// Playwright: await context.addInitScript({ path: ".../lightspeed-demo-pointer.js" });
(() => {
  const install = () => {
    if (document.getElementById("lightspeed-demo-pointer")) return;
    const pointer = document.createElement("div");
    pointer.id = "lightspeed-demo-pointer";
    Object.assign(pointer.style, {
      position: "fixed",
      left: "0",
      top: "0",
      width: "30px",
      height: "36px",
      zIndex: "2147483647",
      pointerEvents: "none",
      display: "none",
      filter: "drop-shadow(1px 2px 2px #0008)",
    });
    pointer.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="30" height="36" viewBox="0 0 30 36"><path d="M2 2 L2 28 L9 21 L15 34 L21 31 L15 19 L26 19 Z" fill="white" stroke="#111" stroke-width="2" stroke-linejoin="round"/></svg>';
    document.documentElement.append(pointer);
    document.addEventListener("mousemove", (event) => {
      pointer.style.display = "block";
      pointer.style.transform = `translate(${event.clientX}px, ${event.clientY}px)`;
    }, { passive: true });
    document.addEventListener("mousedown", (event) => {
      const ring = document.createElement("div");
      Object.assign(ring.style, {
        position: "fixed",
        left: `${event.clientX - 18}px`,
        top: `${event.clientY - 18}px`,
        width: "36px",
        height: "36px",
        border: "3px solid #ffbe0b",
        borderRadius: "50%",
        boxSizing: "border-box",
        background: "#ffbe0b33",
        zIndex: "2147483646",
        pointerEvents: "none",
      });
      document.documentElement.append(ring);
      setTimeout(() => ring.remove(), 650);
    }, { passive: true });
    window.addEventListener("blur", () => { pointer.style.display = "none"; });
  };
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", install, { once: true });
  } else {
    install();
  }
})();
