const installers = {
  brew: "brew install iml885203/tap/tunlease",
  scoop: "scoop bucket add tunlease https://github.com/iml885203/scoop-bucket && scoop install tunlease",
  direct: "curl -fsSL https://raw.githubusercontent.com/iml885203/tunlease/main/scripts/install.sh | bash",
};

document.querySelectorAll("[data-install]").forEach((tab) => {
  tab.addEventListener("click", () => {
    document.querySelectorAll("[data-install]").forEach((item) => {
      item.setAttribute("aria-selected", String(item === tab));
    });
    document.querySelector("#install-command").textContent = installers[tab.dataset.install];
  });
});

document.querySelectorAll("[data-copy-target]").forEach((button) => {
  button.addEventListener("click", async () => {
    const value = document.querySelector(`#${button.dataset.copyTarget}`).textContent;
    try {
      await navigator.clipboard.writeText(value);
      button.textContent = "Copied";
      button.classList.add("copied");
      window.setTimeout(() => {
        button.textContent = "Copy";
        button.classList.remove("copied");
      }, 1600);
    } catch {
      button.textContent = "Select";
    }
  });
});
