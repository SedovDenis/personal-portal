"use strict";

// Confirm system power actions explicitly before sending a POST request.
document.querySelectorAll("form[data-dashboard-power]").forEach((form) => {
    form.addEventListener("submit", (event) => {
        const action = form.dataset.dashboardPower;
        const message = action === "poweroff"
            ? "Выключить домашний сервер? Веб-портал, SSH и другие службы станут недоступны до его включения."
            : "Перезагрузить домашний сервер? Все текущие соединения будут прерваны.";

        if (!window.confirm(message)) {
            event.preventDefault();
            return;
        }

        const button = form.querySelector('button[type="submit"]');
        if (button) {
            button.disabled = true;
            button.textContent = "Команда отправляется…";
        }
    });
});
