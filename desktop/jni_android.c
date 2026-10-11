#include <stdlib.h>
#include <string.h>
#include "jni_android.h"

static JavaVM *kvm = NULL;
static jclass kcls = NULL;

void kantra_store(JNIEnv *env, jclass cls) {
	if (kcls != NULL) return;
	(*env)->GetJavaVM(env, &kvm);
	// FindClass from native threads would use the system class loader and
	// not find app classes, hence a global reference taken now.
	kcls = (jclass)(*env)->NewGlobalRef(env, cls);
}

static JNIEnv *attach(int *detach) {
	JNIEnv *env = NULL;
	*detach = 0;
	if (kvm == NULL) return NULL;
	if ((*kvm)->GetEnv(kvm, (void **)&env, JNI_VERSION_1_6) == JNI_EDETACHED) {
		if ((*kvm)->AttachCurrentThread(kvm, &env, NULL) != 0) return NULL;
		*detach = 1;
	}
	return env;
}

static void done(JNIEnv *env, int detach) {
	if ((*env)->ExceptionCheck(env)) {
		(*env)->ExceptionDescribe(env);
		(*env)->ExceptionClear(env);
	}
	if (detach) (*kvm)->DetachCurrentThread(kvm);
}

void kantra_call(const char *method, const char *arg) {
	int detach;
	JNIEnv *env = attach(&detach);
	if (env == NULL || kcls == NULL) return;
	jmethodID mid = (*env)->GetStaticMethodID(env, kcls, method, "(Ljava/lang/String;)V");
	if (mid != NULL) {
		jstring jarg = (*env)->NewStringUTF(env, arg);
		(*env)->CallStaticVoidMethod(env, kcls, mid, jarg);
		(*env)->DeleteLocalRef(env, jarg);
	}
	done(env, detach);
}

char *kantra_call_str(const char *method, const char *arg) {
	int detach;
	char *out = NULL;
	JNIEnv *env = attach(&detach);
	if (env == NULL || kcls == NULL) return NULL;
	jmethodID mid = (*env)->GetStaticMethodID(env, kcls, method, "(Ljava/lang/String;)Ljava/lang/String;");
	if (mid != NULL) {
		jstring jarg = (*env)->NewStringUTF(env, arg);
		jstring res = (jstring)(*env)->CallStaticObjectMethod(env, kcls, mid, jarg);
		(*env)->DeleteLocalRef(env, jarg);
		if (res != NULL && !(*env)->ExceptionCheck(env)) {
			out = kantra_jstring(env, res);
			(*env)->DeleteLocalRef(env, res);
		}
	}
	done(env, detach);
	return out;
}

char *kantra_jstring(JNIEnv *env, jstring s) {
	if (s == NULL) return NULL;
	const char *utf = (*env)->GetStringUTFChars(env, s, NULL);
	if (utf == NULL) return NULL;
	char *out = strdup(utf);
	(*env)->ReleaseStringUTFChars(env, s, utf);
	return out;
}
