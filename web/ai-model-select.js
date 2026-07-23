/* Keep provider-owned model, key hint, and saved-state controls in sync. */
(function () {
  var options = window.aiModelOptions || {};
  var providers = window.aiProviderOptions || [];
  var saved = window.aiKeySavedByProvider || {};
  var provider = document.getElementById('ai-provider-select');
  var model = document.getElementById('ai-model-select');
  var key = document.getElementById('ai-key-input');
  var status = document.getElementById('ai-key-status');
  var deletion = document.getElementById('ai-key-delete');
  var deleteProvider = document.getElementById('ai-key-delete-provider');
  var deleteConfirm = document.getElementById('ai-key-delete-confirm');
  if (!provider || !model) return;

  function updateCredentialState() {
    var info = providers.find(function (item) { return item.id === provider.value; });
    var hasKey = !!saved[provider.value];
    if (key) key.placeholder = hasKey ? '변경하려면 새 키 입력' : (info ? info.keyPlaceholder : '');
    if (status) {
      status.textContent = hasKey
        ? '현재 •••• 저장됨 — 바꾸려면 새 키를 입력하세요. 비워두면 그대로 둬요.'
        : (provider.value
          ? '저장된 키가 없어 AI를 사용할 수 없어요. 새 키를 입력해주세요.'
          : '제공자를 선택한 뒤 발급받은 키를 붙여넣으세요.');
    }
    if (deletion) deletion.hidden = !hasKey;
    if (deleteProvider) deleteProvider.value = provider.value;
    if (deleteConfirm) deleteConfirm.checked = false;
  }

  provider.addEventListener('change', function () {
    var models = options[provider.value] || [];
    model.innerHTML = '';

    var def = document.createElement('option');
    def.value = '';
    def.textContent = '기본값';
    model.appendChild(def);

    models.forEach(function (id) {
      var opt = document.createElement('option');
      opt.value = id;
      opt.textContent = id;
      model.appendChild(opt);
    });
    model.value = '';
    updateCredentialState();
  });
  updateCredentialState();
})();
