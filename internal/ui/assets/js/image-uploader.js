document.addEventListener('alpine:init', () => {
  Alpine.data('imageUploader', (productId) => ({
    uploading: false,
    dragging: false,
    progress: '',
    error: '',

    handleDrop(event) {
      const files = event.dataTransfer.files;
      if (files.length) {
        this.$refs.fileInput.files = files;
        this.handleFiles({ target: { files, value: '' } });
      }
    },

    async handleFiles(event) {
      const files = event.target.files;
      if (!files.length) return;

      this.uploading = true;
      this.error = '';

      for (let i = 0; i < files.length; i++) {
        this.progress = (i + 1) + ' of ' + files.length;
        try {
          await this.uploadFile(files[i], i);
        } catch (err) {
          this.error = 'Upload failed: ' + err.message;
          break;
        }
      }

      this.uploading = false;
      this.progress = '';
      if (event.target.value !== undefined) {
        event.target.value = '';
      }
    },

    async uploadFile(file, position) {
      const urlForm = new FormData();
      urlForm.append('content_type', file.type || 'image/jpeg');
      const urlResp = await fetch('/admin/images/upload-url', {
        method: 'POST',
        headers: { 'X-Requested-With': 'XMLHttpRequest' },
        body: urlForm,
      });
      if (!urlResp.ok) throw new Error('Failed to get upload URL');
      const { upload_url, r2_key } = await urlResp.json();

      const r2Resp = await fetch(upload_url, {
        method: 'PUT',
        headers: { 'Content-Type': file.type || 'image/jpeg' },
        body: file,
      });
      if (!r2Resp.ok) throw new Error('Failed to upload to R2');

      const persistForm = new FormData();
      persistForm.append('r2_key', r2_key);
      persistForm.append('alt_text', file.name.replace(/\.[^.]+$/, ''));
      persistForm.append('position', position.toString());

      const persistResp = await fetch('/admin/catalog/' + productId + '/images', {
        method: 'POST',
        headers: { 'HX-Request': 'true' },
        body: persistForm,
      });
      if (!persistResp.ok) throw new Error('Failed to save image');

      const html = await persistResp.text();
      const gallery = document.getElementById('media-gallery');
      if (gallery) {
        gallery.outerHTML = html;
        htmx.process(document.getElementById('media-gallery'));
      }
    },
  }));
});
