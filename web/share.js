// File-drop page: drag and drop plus per-file progress. The form still works without JS.
(() => {
  const form = document.getElementById('drop');
  if (!form) return;
  const input = document.getElementById('files');
  const zone = form.querySelector('.zone');
  const queue = document.getElementById('queue');
  const send = document.getElementById('send');
  const max = Number(form.dataset.max || 0);
  let files = [];

  const fmt = n => n < 1024 ? n + ' B' : n < 1 << 20 ? (n / 1024).toFixed(1) + ' KB' : n < 1 << 30 ? (n / 1048576).toFixed(1) + ' MB' : (n / 1073741824).toFixed(2) + ' GB';

  function render() {
    queue.textContent = '';
    files.forEach(f => {
      const li = document.createElement('li');
      const name = document.createElement('span');
      name.textContent = f.name;
      const size = document.createElement('span');
      size.className = 'muted num';
      size.textContent = max && f.size > max ? 'too large' : fmt(f.size);
      if (max && f.size > max) size.className = 'err';
      const bar = document.createElement('progress');
      bar.max = 1; bar.value = 0;
      li.append(name, size, bar);
      f._bar = bar; f._size = size;
      queue.append(li);
    });
  }

  input.addEventListener('change', () => { files = [...input.files]; render(); });
  ['dragenter', 'dragover'].forEach(t => zone.addEventListener(t, e => { e.preventDefault(); zone.classList.add('over'); }));
  ['dragleave', 'drop'].forEach(t => zone.addEventListener(t, e => { e.preventDefault(); zone.classList.remove('over'); }));
  zone.addEventListener('drop', e => { files = [...e.dataTransfer.files]; render(); });

  form.addEventListener('submit', async e => {
    e.preventDefault();
    const ok = files.filter(f => !max || f.size <= max);
    if (!ok.length) { input.click(); return; }
    send.disabled = true;
    let sent = 0;
    for (const f of ok) {
      const fd = new FormData();
      fd.append('files', f, f.name);
      const good = await new Promise(res => {
        const x = new XMLHttpRequest();
        x.open('POST', form.action);
        x.setRequestHeader('Accept', 'application/json');
        x.upload.onprogress = ev => { if (ev.lengthComputable) f._bar.value = ev.loaded / ev.total; };
        x.onload = () => res(x.status === 200);
        x.onerror = () => res(false);
        x.send(fd);
      });
      f._bar.value = good ? 1 : 0;
      f._size.textContent = good ? 'sent' : 'failed, try again';
      f._size.className = good ? 'done' : 'err';
      if (good) sent++;
    }
    send.disabled = false;
    send.textContent = sent === ok.length ? 'Send more files' : 'Retry';
    files = files.filter(f => f._size.className !== 'done');
    if (form.dataset.reload && sent) setTimeout(() => location.reload(), 600);
    input.value = '';
  });
})();
