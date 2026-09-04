#include "gst.h"

#include <dlfcn.h>

#define CUDA_ERROR_NO_DEVICE 100

typedef int CUdevice;
typedef void *CUcontext;
typedef int CUresult;

typedef CUresult (*CuInit)(unsigned int flags);
typedef CUresult (*CuDeviceGetCount)(int *count);
typedef CUresult (*CuDeviceGet)(CUdevice *device, int ordinal);
typedef CUresult (*CuCtxCreate)(CUcontext *context, unsigned int flags, CUdevice device);
typedef CUresult (*CuCtxDestroy)(CUcontext context);
typedef CUresult (*CuGetErrorName)(CUresult error, const char **name);

static int cuda_probe_result(void *library, CUresult code, const char *stage,
    CuGetErrorName getErrorName, char **resultStage, char **errorName) {
  const char *name = NULL;
  if (getErrorName(code, &name) != 0 || name == NULL) {
    name = "CUDA_ERROR_UNKNOWN";
  }

  *resultStage = g_strdup(stage);
  *errorName = g_strdup(name);
  dlclose(library);
  return code;
}

int gstreamer_cuda_context_probe(char **stage, char **errorName) {
  void *library = dlopen("libcuda.so.1", RTLD_LAZY | RTLD_LOCAL);
  if (library == NULL) {
    *stage = g_strdup("loading the CUDA driver");
    *errorName = g_strdup("CUDA_DRIVER_LIBRARY_UNAVAILABLE");
    return -1;
  }

  CuInit cuInit = (CuInit)dlsym(library, "cuInit");
  CuDeviceGetCount cuDeviceGetCount = (CuDeviceGetCount)dlsym(library, "cuDeviceGetCount");
  CuDeviceGet cuDeviceGet = (CuDeviceGet)dlsym(library, "cuDeviceGet");
  CuCtxCreate cuCtxCreate = (CuCtxCreate)dlsym(library, "cuCtxCreate_v2");
  CuCtxDestroy cuCtxDestroy = (CuCtxDestroy)dlsym(library, "cuCtxDestroy_v2");
  CuGetErrorName cuGetErrorName = (CuGetErrorName)dlsym(library, "cuGetErrorName");
  if (cuInit == NULL || cuDeviceGetCount == NULL || cuDeviceGet == NULL ||
      cuCtxCreate == NULL || cuCtxDestroy == NULL || cuGetErrorName == NULL) {
    *stage = g_strdup("resolving CUDA driver symbols");
    *errorName = g_strdup("CUDA_DRIVER_SYMBOL_UNAVAILABLE");
    dlclose(library);
    return -2;
  }

  CUresult result = cuInit(0);
  if (result != 0) {
    return cuda_probe_result(library, result, "initializing CUDA", cuGetErrorName, stage, errorName);
  }

  int deviceCount = 0;
  result = cuDeviceGetCount(&deviceCount);
  if (result != 0) {
    return cuda_probe_result(library, result, "querying CUDA devices", cuGetErrorName, stage, errorName);
  }
  if (deviceCount == 0) {
    return cuda_probe_result(library, CUDA_ERROR_NO_DEVICE, "querying CUDA devices", cuGetErrorName, stage, errorName);
  }

  CUdevice device;
  result = cuDeviceGet(&device, 0);
  if (result != 0) {
    return cuda_probe_result(library, result, "selecting a CUDA device", cuGetErrorName, stage, errorName);
  }

  CUcontext context;
  result = cuCtxCreate(&context, 0, device);
  if (result != 0) {
    return cuda_probe_result(library, result, "creating a CUDA context", cuGetErrorName, stage, errorName);
  }

  cuCtxDestroy(context);
  return cuda_probe_result(library, 0, "creating a CUDA context", cuGetErrorName, stage, errorName);
}

static void gstreamer_pipeline_log(GstPipelineCtx *ctx, char* level, const char* format, ...) {
  va_list argptr;
  va_start(argptr, format);
  char buffer[100];
  vsprintf(buffer, format, argptr);
  va_end(argptr);
  goPipelineLog(ctx->pipelineId, level, buffer);
}

static gboolean gstreamer_bus_call(GstBus *bus, GstMessage *msg, gpointer user_data) {
  GstPipelineCtx *ctx = (GstPipelineCtx *)user_data;

  switch (GST_MESSAGE_TYPE(msg)) {
    case GST_MESSAGE_EOS: {
      gstreamer_pipeline_log(ctx, "fatal", "end of stream");
      break;
    }

    case GST_MESSAGE_STATE_CHANGED: {
      GstState old_state, new_state;
      gst_message_parse_state_changed(msg, &old_state, &new_state, NULL);

      gstreamer_pipeline_log(ctx, "debug",
        "element %s changed state from %s to %s",
          GST_OBJECT_NAME(msg->src),
          gst_element_state_get_name(old_state),
          gst_element_state_get_name(new_state));
      break;
    }

    case GST_MESSAGE_TAG: {
      GstTagList *tags = NULL;
      gst_message_parse_tag(msg, &tags);

      gstreamer_pipeline_log(ctx, "debug",
        "got tags from element %s",
          GST_OBJECT_NAME(msg->src));

      gst_tag_list_unref(tags);
      break;
    }

    case GST_MESSAGE_ERROR: {
      GError *err = NULL;
      gchar *dbg_info = NULL;
      gst_message_parse_error(msg, &err, &dbg_info);

      gstreamer_pipeline_log(ctx, "error",
        "error from element %s: %s",
          GST_OBJECT_NAME(msg->src), err->message);
      gstreamer_pipeline_log(ctx, "warn",
        "debugging info: %s",
          (dbg_info) ? dbg_info : "none");

      g_error_free(err);
      g_free(dbg_info);
      break;
    }

    default:
      gstreamer_pipeline_log(ctx, "trace", "unknown message");
      break;
  }

  return TRUE;
}

GstPipelineCtx *gstreamer_pipeline_create(char *pipelineStr, int pipelineId, GError **error) {
  GstElement *pipeline = gst_parse_launch(pipelineStr, error);
  if (pipeline == NULL) return NULL;

  // create gstreamer pipeline context
  GstPipelineCtx *ctx = calloc(1, sizeof(GstPipelineCtx));
  ctx->pipelineId = pipelineId;
  ctx->pipeline = pipeline;

  GstBus *bus = gst_pipeline_get_bus(GST_PIPELINE(pipeline));
  gst_bus_add_watch(bus, gstreamer_bus_call, ctx);
  gst_object_unref(bus);

  return ctx;
}

static GstFlowReturn gstreamer_send_new_sample_handler(GstElement *object, gpointer user_data) {
  GstPipelineCtx *ctx = (GstPipelineCtx *)user_data;
  GstSample *sample = NULL;
  GstBuffer *buffer = NULL;
  gpointer copy = NULL;
  gsize copy_size = 0;

  g_signal_emit_by_name(object, "pull-sample", &sample);
  if (sample) {
    buffer = gst_sample_get_buffer(sample);
    if (buffer) {
      gst_buffer_extract_dup(buffer, 0, gst_buffer_get_size(buffer), &copy, &copy_size);
      goHandlePipelineBuffer(ctx->pipelineId, copy, copy_size,
        GST_BUFFER_DURATION(buffer),
        GST_BUFFER_FLAG_IS_SET(buffer, GST_BUFFER_FLAG_DELTA_UNIT)
      );
    }
    gst_sample_unref(sample);
  }

  return GST_FLOW_OK;
}

void gstreamer_pipeline_attach_appsink(GstPipelineCtx *ctx, char *sinkName) {
  ctx->appsink = gst_bin_get_by_name(GST_BIN(ctx->pipeline), sinkName);
  g_object_set(ctx->appsink, "emit-signals", TRUE, NULL);
  g_signal_connect(ctx->appsink, "new-sample", G_CALLBACK(gstreamer_send_new_sample_handler), ctx);
}

void gstreamer_pipeline_attach_appsrc(GstPipelineCtx *ctx, char *srcName) {
  ctx->appsrc = gst_bin_get_by_name(GST_BIN(ctx->pipeline), srcName);
}

void gstreamer_pipeline_play(GstPipelineCtx *ctx) {
  gst_element_set_state(GST_ELEMENT(ctx->pipeline), GST_STATE_PLAYING);
}

void gstreamer_pipeline_pause(GstPipelineCtx *ctx) {
  gst_element_set_state(GST_ELEMENT(ctx->pipeline), GST_STATE_PAUSED);
}

void gstreamer_pipeline_destory(GstPipelineCtx *ctx) {
  // end appsrc, if exists
  if (ctx->appsrc) {
    gst_app_src_end_of_stream(GST_APP_SRC(ctx->appsrc));
  }

  // send pipeline eos
  gst_element_send_event(GST_ELEMENT(ctx->pipeline), gst_event_new_eos());

  // set null state
  gst_element_set_state(GST_ELEMENT(ctx->pipeline), GST_STATE_NULL);

  if (ctx->appsink) {
    gst_object_unref(ctx->appsink);
    ctx->appsink = NULL;
  }

  if (ctx->appsrc) {
    gst_object_unref(ctx->appsrc);
    ctx->appsrc = NULL;
  }

  gst_object_unref(ctx->pipeline);
}

void gstreamer_pipeline_push(GstPipelineCtx *ctx, void *buffer, int bufferLen) {
  if (ctx->appsrc != NULL) {
    gpointer p = g_memdup2(buffer, bufferLen);
    GstBuffer *buffer = gst_buffer_new_wrapped(p, bufferLen);
    gst_app_src_push_buffer(GST_APP_SRC(ctx->appsrc), buffer);
  }
}

gboolean gstreamer_pipeline_set_prop_int(GstPipelineCtx *ctx, char *binName, char *prop, gint value) {
  GstElement *el = gst_bin_get_by_name(GST_BIN(ctx->pipeline), binName);
  if (el == NULL) return FALSE;

  g_object_set(G_OBJECT(el),
    prop, value,
    NULL);

  gst_object_unref(el);
  return TRUE;
}

gboolean gstreamer_pipeline_set_caps_framerate(GstPipelineCtx *ctx, const gchar* binName, gint numerator, gint denominator) {
  GstElement *el = gst_bin_get_by_name(GST_BIN(ctx->pipeline), binName);
  if (el == NULL) return FALSE;

  GstCaps *caps = gst_caps_new_simple("video/x-raw",
    "framerate", GST_TYPE_FRACTION, numerator, denominator,
    NULL);

  g_object_set(G_OBJECT(el),
    "caps", caps,
    NULL);

  gst_caps_unref(caps);
  gst_object_unref(el);
  return TRUE;
}

gboolean gstreamer_pipeline_set_caps_resolution(GstPipelineCtx *ctx, const gchar* binName, gint width, gint height) {
  GstElement *el = gst_bin_get_by_name(GST_BIN(ctx->pipeline), binName);
  if (el == NULL) return FALSE;

  GstCaps *caps = gst_caps_new_simple("video/x-raw",
    "width", G_TYPE_INT, width,
    "height", G_TYPE_INT, height,
    NULL);

  g_object_set(G_OBJECT(el),
    "caps", caps,
    NULL);

  gst_caps_unref(caps);
  gst_object_unref(el);
  return TRUE;
}

gboolean gstreamer_pipeline_emit_video_keyframe(GstPipelineCtx *ctx) {
	GstClock *clock = gst_pipeline_get_clock(GST_PIPELINE(ctx->pipeline));
	gst_object_ref(clock);

	GstClockTime time = gst_clock_get_time(clock);
	GstClockTime now = time - gst_element_get_base_time(ctx->pipeline);
	gst_object_unref(clock);

	GstEvent *keyFrameEvent = gst_video_event_new_downstream_force_key_unit(now, time, now, TRUE, 0);
	return gst_element_send_event(GST_ELEMENT(ctx->pipeline), keyFrameEvent);
}
