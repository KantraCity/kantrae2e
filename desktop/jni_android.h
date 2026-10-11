// JNI glue between desktop/jni_android.go and com.wails.app.KantraNative.
#include <jni.h>

void kantra_store(JNIEnv *env, jclass cls);
// Calls KantraNative.<method>(String) (void). Safe from any thread.
void kantra_call(const char *method, const char *arg);
// Calls KantraNative.<method>(String) returning String; result is malloc'd
// (free it) or NULL.
char *kantra_call_str(const char *method, const char *arg);
// Copies a Java string (malloc'd, NULL for null).
char *kantra_jstring(JNIEnv *env, jstring s);
