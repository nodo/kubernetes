package fieldmanager

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/util/proto"
	prototesting "k8s.io/kube-openapi/pkg/util/proto/testing"
	"sigs.k8s.io/structured-merge-diff/v4/fieldpath"
	"sigs.k8s.io/structured-merge-diff/v4/merge"
	"sigs.k8s.io/structured-merge-diff/v4/typed"
)

var kubernetesSwaggerSchema = prototesting.Fake{
	Path: filepath.Join(
		strings.Repeat(".."+string(filepath.Separator), 8),
		"api", "openapi-spec", "swagger.json"),
}

type fakeObjectConvertor struct {
	converter  merge.Converter
	apiVersion fieldpath.APIVersion
}

func (c *fakeObjectConvertor) Convert(in, out, context interface{}) error {
	if typedValue, ok := in.(*typed.TypedValue); ok {
		var err error
		out, err = c.converter.Convert(typedValue, c.apiVersion)
		return err
	}
	out = in
	return nil
}

func (c *fakeObjectConvertor) ConvertToVersion(in runtime.Object, gv runtime.GroupVersioner) (runtime.Object, error) {
	return in, nil
}

func (c *fakeObjectConvertor) ConvertFieldLabel(_ schema.GroupVersionKind, _, _ string) (string, string, error) {
	return "", "", errors.New("not implemented")
}

type fakeObjectDefaulter struct{}

func (d *fakeObjectDefaulter) Default(in runtime.Object) {}

type TestFieldManager struct {
	fieldManager *FieldManager
	emptyObj     runtime.Object
	liveObj      runtime.Object
}

func NewDefaultTestFieldManager(gvk schema.GroupVersionKind) TestFieldManager {
	return NewTestFieldManager(gvk, false, nil)
}

func NewSubresourceTestFieldManager(gvk schema.GroupVersionKind) TestFieldManager {
	return NewTestFieldManager(gvk, true, nil)
}

func NewTestFieldManagerWithOpenAPIModel(gvk schema.GroupVersionKind, ignoreManagedFieldsFromRequestObject bool, chainFieldManager func(Manager) Manager, m proto.Models) TestFieldManager {
	typeConverter := NewFakeTypeConverter(m)
	converter := newVersionConverter(typeConverter, &fakeObjectConvertor{}, gvk.GroupVersion())
	apiVersion := fieldpath.APIVersion(gvk.GroupVersion().String())
	objectConverter := &fakeObjectConvertor{converter, apiVersion}
	f, err := NewStructuredMergeManager(
		typeConverter,
		objectConverter,
		&fakeObjectDefaulter{},
		gvk.GroupVersion(),
		gvk.GroupVersion(),
	)
	if err != nil {
		panic(err)
	}
	live := &unstructured.Unstructured{}
	live.SetKind(gvk.Kind)
	live.SetAPIVersion(gvk.GroupVersion().String())
	f = NewStripMetaManager(f)
	f = NewManagedFieldsUpdater(f)
	f = NewBuildManagerInfoManager(f, gvk.GroupVersion())
	f = NewProbabilisticSkipNonAppliedManager(f, &fakeObjectCreater{gvk: gvk}, gvk, DefaultTrackOnCreateProbability)
	f = NewLastAppliedManager(f, typeConverter, objectConverter, gvk.GroupVersion())
	f = NewLastAppliedUpdater(f)
	if chainFieldManager != nil {
		f = chainFieldManager(f)
	}
	return TestFieldManager{
		fieldManager: NewFieldManager(f, ignoreManagedFieldsFromRequestObject),
		emptyObj:     live,
		liveObj:      live.DeepCopyObject(),
	}
}

func NewTestFieldManager(gvk schema.GroupVersionKind, ignoreManagedFieldsFromRequestObject bool, chainFieldManager func(Manager) Manager) TestFieldManager {
	m := NewFakeOpenAPIModels()
	return NewTestFieldManagerWithOpenAPIModel(gvk, ignoreManagedFieldsFromRequestObject, chainFieldManager, m)
}

func NewFakeTypeConverter(m proto.Models) TypeConverter {
	tc, err := NewTypeConverter(m, false)
	if err != nil {
		panic(fmt.Sprintf("Failed to build TypeConverter: %v", err))
	}
	return tc
}

func NewFakeOpenAPIModels() proto.Models {
	d, err := kubernetesSwaggerSchema.OpenAPISchema()
	if err != nil {
		panic(err)
	}
	m, err := proto.NewOpenAPIData(d)
	if err != nil {
		panic(err)
	}
	return m
}

func (f *TestFieldManager) Reset() {
	f.liveObj = f.emptyObj.DeepCopyObject()
}

func (f *TestFieldManager) Apply(obj runtime.Object, manager string, force bool) error {
	out, err := f.fieldManager.Apply(f.liveObj, obj, manager, force)
	if err == nil {
		f.liveObj = out
	}
	return err
}

func (f *TestFieldManager) Update(obj runtime.Object, manager string) error {
	out, err := f.fieldManager.Update(f.liveObj, obj, manager)
	if err == nil {
		f.liveObj = out
	}
	return err
}

func (f *TestFieldManager) ManagedFields() []metav1.ManagedFieldsEntry {
	accessor, err := meta.Accessor(f.liveObj)
	if err != nil {
		panic(fmt.Errorf("couldn't get accessor: %v", err))
	}

	return accessor.GetManagedFields()
}

func (f *TestFieldManager) FieldManager() *FieldManager {
	return f.fieldManager
}

type fakeObjectCreater struct {
	gvk schema.GroupVersionKind
}

var _ runtime.ObjectCreater = &fakeObjectCreater{}

func (f *fakeObjectCreater) New(_ schema.GroupVersionKind) (runtime.Object, error) {
	u := unstructured.Unstructured{Object: map[string]interface{}{}}
	u.SetAPIVersion(f.gvk.GroupVersion().String())
	u.SetKind(f.gvk.Kind)
	return &u, nil
}
