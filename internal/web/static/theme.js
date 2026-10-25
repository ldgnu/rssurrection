/* Aplica el tema antes del primer pintado. */
(function () {
  try {
    var t = localStorage.getItem('rssr-theme') || 'auto';
    document.documentElement.setAttribute('data-theme', t);
  } catch (e) {}
})();
